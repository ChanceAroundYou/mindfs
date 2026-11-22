// 同步通道的请求认证：bearer token + 时间戳 + nonce + HMAC-SHA256。
//
// 为什么不是单纯 bearer：这条通道搬的是整个控制面，users 域的载荷里带 bcrypt
// 口令 hash。token 一旦出现在代理日志、shell history 或抓包里，光靠它就能被
// 重放着灌数据；加上时间窗与 nonce 之后，抓到的请求在 5 分钟后失效、同一条
// 请求只能成功一次。
//
// 签名覆盖 timestamp + nonce + body（spec §6.3 / D4），不覆盖 instance 头：
// instance 是审计与对账用的自称，真正的身份凭证是 token。
package controlsync

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	SyncInstanceHeader  = "X-MindFS-Instance"
	SyncTimestampHeader = "X-MindFS-Timestamp"
	SyncNonceHeader     = "X-MindFS-Nonce"
	SyncSignatureHeader = "X-MindFS-Signature"

	// SyncClockSkew 是时间戳允许的双向偏差。两台机器不共享时钟，5 分钟足够
	// 吸收 NTP 漂移，又把抓包重放的窗口压到可接受范围。
	SyncClockSkew = 5 * time.Minute
	// SyncNonceTTL 略大于两倍偏差：只要请求还能通过时间窗，nonce 就还在缓存里。
	SyncNonceTTL = 10 * time.Minute
	// SyncMaxBodyBytes 与 push handler 的 8 MiB 上限同源；认证层先读一遍 body，
	// 超限必须在这里就拒，不能把 8 MiB 直接读进内存。
	SyncMaxBodyBytes = 8 << 20

	syncNonceMinLen     = 8
	syncNonceMaxLen     = 128
	syncNonceCacheLimit = 65536
)

// ErrSyncUnauthorized 是所有认证失败的统一出口。具体原因只进服务端日志，
// 不回给调用方 —— 告诉攻击者「签名对了但 nonce 重放」等于送他一次调参机会。
var ErrSyncUnauthorized = errors.New("controlsync: sync request unauthorized")

// SyncSignature 计算请求签名：HMAC-SHA256(token, timestamp + "\n" + nonce + "\n" + body)。
// 换行分隔保证字段边界不可歧义（否则 timestamp="a", nonce="b\nc" 与
// timestamp="a\nb", nonce="c" 会撞出同一个串）。
func SyncSignature(token, timestamp, nonce string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("\n"))
	mac.Write([]byte(nonce))
	mac.Write([]byte("\n"))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// SignSyncRequest 给一个出站请求写好四个认证头。body 必须是即将发送的原始字节：
// 签名与发送各序列化一次会让两者微妙地不一致（键序、空格），表现为随机的 401。
func SignSyncRequest(req *http.Request, token, instanceID string, body []byte, now time.Time) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("controlsync: sync token required")
	}
	if req == nil {
		return errors.New("controlsync: request required")
	}
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return err
	}
	timestamp := now.UTC().Format(time.RFC3339)
	nonce := hex.EncodeToString(nonceBytes)

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(SyncTimestampHeader, timestamp)
	req.Header.Set(SyncNonceHeader, nonce)
	req.Header.Set(SyncSignatureHeader, SyncSignature(token, timestamp, nonce, body))
	if instanceID = strings.TrimSpace(instanceID); instanceID != "" {
		req.Header.Set(SyncInstanceHeader, instanceID)
	} else {
		req.Header.Del(SyncInstanceHeader)
	}
	return nil
}

// SyncVerifyOptions 把可注入的部分单独拿出来：测试要固定时钟与共享 nonce 缓存。
type SyncVerifyOptions struct {
	Token  string
	Nonces *NonceCache
	Now    func() time.Time
}

// VerifySyncRequest 按固定顺序校验：bearer → 时间戳 → nonce 形状 → 签名 → 记账。
//
// nonce 只在签名通过之后才写入缓存，这个顺序不能换：先记账再验签，任何人随手
// 发一个坏签名就能把某个 nonce 烧掉，合法请求反而被判重放。
func VerifySyncRequest(r *http.Request, body []byte, opts SyncVerifyOptions) error {
	if r == nil || !AuthorizeSyncRequest(opts.Token, r.Header.Get("Authorization")) {
		return ErrSyncUnauthorized
	}
	timestamp := strings.TrimSpace(r.Header.Get(SyncTimestampHeader))
	nonce := strings.TrimSpace(r.Header.Get(SyncNonceHeader))
	signature := strings.TrimSpace(r.Header.Get(SyncSignatureHeader))
	if timestamp == "" || nonce == "" || signature == "" {
		return ErrSyncUnauthorized
	}
	at, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return ErrSyncUnauthorized
	}
	now := time.Now()
	if opts.Now != nil {
		now = opts.Now()
	}
	if skew := now.Sub(at); skew > SyncClockSkew || skew < -SyncClockSkew {
		return ErrSyncUnauthorized
	}
	if !validSyncNonce(nonce) {
		return ErrSyncUnauthorized
	}
	got, err := hex.DecodeString(signature)
	if err != nil {
		return ErrSyncUnauthorized
	}
	want, err := hex.DecodeString(SyncSignature(strings.TrimSpace(opts.Token), timestamp, nonce, body))
	if err != nil {
		return ErrSyncUnauthorized
	}
	if !hmac.Equal(got, want) {
		return ErrSyncUnauthorized
	}
	if opts.Nonces != nil && !opts.Nonces.Accept(nonce, now) {
		return ErrSyncUnauthorized
	}
	return nil
}

// validSyncNonce 只做形状校验（长度 + 可见 ASCII），不限定具体编码：
// 未来换成 base64url 或 UUID 不用改服务端。
func validSyncNonce(nonce string) bool {
	if len(nonce) < syncNonceMinLen || len(nonce) > syncNonceMaxLen {
		return false
	}
	for _, r := range nonce {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

// NonceCache 记住最近见过的 nonce 直到 TTL 到期。进程重启会丢失缓存，但时间窗
// 仍然挡着重放：攻击者最多把一条旧请求重放一次，且必须在重启后 5 分钟内。
type NonceCache struct {
	mu        sync.Mutex
	ttl       time.Duration
	limit     int
	seen      map[string]time.Time
	nextSweep time.Time
}

func NewNonceCache(ttl time.Duration) *NonceCache {
	if ttl <= 0 {
		ttl = SyncNonceTTL
	}
	return &NonceCache{
		ttl:   ttl,
		limit: syncNonceCacheLimit,
		seen:  make(map[string]time.Time),
	}
}

// Accept 返回 true 表示这个 nonce 第一次出现（或已过 TTL），false 表示重放。
func (c *NonceCache) Accept(nonce string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now.Before(c.nextSweep) {
		if exp, ok := c.seen[nonce]; ok && now.Before(exp) {
			return false
		}
	} else {
		c.sweepLocked(now)
		if _, ok := c.seen[nonce]; ok {
			return false
		}
	}
	if len(c.seen) >= c.limit {
		c.evictOldestLocked(c.limit / 8)
	}
	c.seen[nonce] = now.Add(c.ttl)
	return true
}

// sweepLocked 清掉过期项，并把下一次清扫推到半个 TTL 之后：每个请求都全表扫
// 一遍在 6 万条时是明显浪费，而清扫间隔远小于 TTL 就不会漏掉活着的 nonce。
func (c *NonceCache) sweepLocked(now time.Time) {
	for nonce, exp := range c.seen {
		if !now.Before(exp) {
			delete(c.seen, nonce)
		}
	}
	c.nextSweep = now.Add(c.ttl / 2)
}

// evictOldestLocked 在缓存打满时按到期时间丢掉最老的一批。正常流量下永远走不到
// 这里（10 分钟窗口内 6 万次请求）；它存在是为了让恶意灌 nonce 也只占有限内存。
func (c *NonceCache) evictOldestLocked(count int) {
	if count <= 0 {
		count = 1
	}
	type entry struct {
		nonce string
		exp   time.Time
	}
	entries := make([]entry, 0, len(c.seen))
	for nonce, exp := range c.seen {
		entries = append(entries, entry{nonce: nonce, exp: exp})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].exp.Before(entries[j].exp) })
	for i := 0; i < count && i < len(entries); i++ {
		delete(c.seen, entries[i].nonce)
	}
}
