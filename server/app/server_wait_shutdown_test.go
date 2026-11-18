package app

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

// 长轮询端点（回复会话等待）会让请求 handler 阻塞到 20s。关停时如果只调
// Shutdown 而不取消请求上下文，Shutdown 必须等这些 handler 自然结束，
// 每次部署/重启都会被拖住。这个测试钉住三件事：
//  1. cancelWait() 之前请求上下文还活着（不是启动即取消的假实现）；
//  2. cancelWait() 之后在途请求 1s 内返回；
//  3. 在途请求消失后 Shutdown 1s 内返回，且 handler 自己观察到了服务端
//     base context 的取消（不是客户端断开）。
func TestWaitAwareServerCancelUnblocksInFlightRequests(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	handlerEntered := make(chan struct{})
	contextCanceled := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(handlerEntered)
		<-r.Context().Done()
		close(contextCanceled)
	})

	srv, cancelWait := newWaitAwareServer(listener.Addr().String(), handler)
	t.Cleanup(func() {
		cancelWait()
		_ = srv.Close()
	})

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve(listener)
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	requestDone := make(chan error, 1)
	go func() {
		resp, err := client.Get("http://" + listener.Addr().String() + "/wait")
		if err == nil {
			_ = resp.Body.Close()
		}
		requestDone <- err
	}()

	select {
	case <-handlerEntered:
	case <-time.After(time.Second):
		t.Fatal("handler did not receive the request within 1s")
	}

	// 关键区分：请求上下文来自 BaseContext，而不是一个启动即取消的 ctx。
	select {
	case <-contextCanceled:
		t.Fatal("request context was canceled before cancelWait() was called")
	default:
	}

	cancelWait()

	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("in-flight request did not return within 1s of cancelWait()")
	}

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- srv.Shutdown(context.Background())
	}()
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not return within 1s of cancelWait()")
	}

	// handler 必须观察到服务端下发的取消，而不是仅仅因为客户端超时/断开。
	select {
	case <-contextCanceled:
	case <-time.After(time.Second):
		t.Fatal("handler context was not canceled after cancelWait()")
	}

	select {
	case <-serveDone:
	case <-time.After(time.Second):
		t.Fatal("Serve did not return within 1s of Shutdown()")
	}
}
