package controlsync

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// wsConn 的全部意义是「把一条 WS 连接伪装成字节流」。这里用一对真实
// gorilla 连接（httptest 起一个 upgrade 端点）来确定这件事真的成立 ——
// 只测自己的 buffer 逻辑证明不了它与 websocket 的边界处理对不对。
func newWSPair(t *testing.T) (client, server *websocket.Conn, cleanup func()) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	ready := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		ready <- c
		<-r.Context().Done()
	}))
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		srv.Close()
		t.Fatalf("dial: %v", err)
	}
	s := <-ready
	return c, s, func() {
		_ = c.Close()
		_ = s.Close()
		srv.Close()
	}
}

// 多条消息在字节流这一层必须无缝拼起来。上层（net/http）会一次字节读里
// 期待拿到任意长度；若消息边界处返回 0 字节，net/http 会把零长度读当成异常。
func TestWSConnReadConcatenatesMessages(t *testing.T) {
	client, server, cleanup := newWSPair(t)
	defer cleanup()
	rc := NewWSConn(server)

	go func() {
		for _, part := range []string{"GET /api/x HT", "TP/1.1\r\nHost: a\r", "\n\r\n"} {
			_ = client.WriteMessage(websocket.BinaryMessage, []byte(part))
		}
		_ = client.WriteMessage(websocket.BinaryMessage, []byte{})
		_ = client.Close()
	}()

	var got bytes.Buffer
	buf := make([]byte, 0, 64)
	tmp := make([]byte, 5) // 故意小于任一消息，逼出跨消息读
	for {
		n, err := rc.Read(tmp)
		got.Write(tmp[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			// 对端 Close 后 gorilla 报的是 CloseError，不是 io.EOF。
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				break
			}
			if strings.Contains(err.Error(), "close") {
				break
			}
			t.Fatalf("read: %v", err)
		}
		_ = buf
	}
	want := "GET /api/x HTTP/1.1\r\nHost: a\r\n\r\n"
	if got.String() != want {
		t.Fatalf("读到的字节流 = %q，want %q", got.String(), want)
	}
}

// 一次 Write 必须落成一条完整消息：对端按消息读时不该看到半条。
func TestWSConnWriteIsOneMessage(t *testing.T) {
	client, server, cleanup := newWSPair(t)
	defer cleanup()
	wc := NewWSConn(client)

	payload := "hello relay"
	n, err := wc.Write([]byte(payload))
	if err != nil || n != len(payload) {
		t.Fatalf("write = (%d, %v)", n, err)
	}
	typ, data, err := server.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.BinaryMessage || string(data) != payload {
		t.Fatalf("message = (%d, %q)，want (binary, %q)", typ, data, payload)
	}
}
