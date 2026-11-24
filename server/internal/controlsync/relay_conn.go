package controlsync

import (
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// wsConn 把一条 websocket 当成 net.Conn 用：把二进制的若干条消息拼成一个
// 连续的字节流。
//
// 为什么需要它：边控端没有可暴露的入口，主控也没法直连它，所以两边唯一的通道
// 是边控端主动拨出的 websocket。但真正要跑的是 HTTP（含 WS 升级），而 Go 的
// net/http 只认 net.Conn —— 有了这一层，边控端可以直接用**本机原来那个
// http.Handler** 服务隧道来的请求，主控那边也可以交给标准库的 Transport，
// 两边的路由、鉴权、升级逻辑一行都不用改。
//
// 消息边界在这里被抹平：上层按字节流读，读到的内容与对端写入的字节完全一致。
// 写侧一条 Write 对应一条二进制消息（不额外缓冲：WS 本身的帧开销远小于额外
// 引入一块缓冲带来的拷贝与延迟）。
type wsConn struct {
	conn *websocket.Conn

	rmu sync.Mutex
	r   io.Reader // 当前消息的读取器；nil 表示需要取下一条消息

	wmu sync.Mutex
}

func NewWSConn(conn *websocket.Conn) *wsConn {
	return &wsConn{conn: conn}
}

// Read 从当前消息里读；一条消息读完就取下一条。
//
// **消息边界绝不能返回 io.EOF**：上层是 net/http，它把 net.Conn 上的 EOF
// 当成「对端关连接」，会在一条消息结束时把整个响应截断（实测 256KB 响应体
// 读到 14KB 就 unexpected EOF）。所以读完一条消息后释放读取器、阻塞等下一条：
// 真正的「对端关了」由 NextReader 自己返回 err。
//
// 返回 (0, nil) 同样不行（net/http 会把零长度读当异常），所以没有拿到字节时继续循环。
func (c *wsConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.rmu.Lock()
	defer c.rmu.Unlock()
	for {
		if c.r == nil {
			_, r, err := c.conn.NextReader()
			if err != nil {
				return 0, err
			}
			c.r = r
		}
		n, err := c.r.Read(p)
		if err == io.EOF {
			c.r = nil
			if n > 0 {
				return n, nil
			}
			continue // 空消息（keepalive 帧之类），取下一条
		}
		return n, err
	}
}

// Write 把一段字节作为一条二进制消息发出去。
//
// 一次 Write 一次 Close：Close 才会把帧刷到对端。中途写失败时也要 Close，
// 否则那半条消息会一直挂在 gorilla 的内部状态里。
func (c *wsConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	w, err := c.conn.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return 0, err
	}
	n, err := w.Write(p)
	if err != nil {
		_ = w.Close()
		return n, err
	}
	if err := w.Close(); err != nil {
		return n, err
	}
	return n, nil
}

func (c *wsConn) Close() error                       { return c.conn.Close() }
func (c *wsConn) LocalAddr() net.Addr                { return c.conn.LocalAddr() }
func (c *wsConn) RemoteAddr() net.Addr               { return c.conn.RemoteAddr() }
func (c *wsConn) SetDeadline(t time.Time) error      { return c.conn.SetReadDeadline(t) }
func (c *wsConn) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *wsConn) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }
