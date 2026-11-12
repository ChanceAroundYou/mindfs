package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	rootfs "mindfs/server/internal/fs"
)

// 切换提示要交给 agent 去读的路径，必须等于会话文件**真正落盘**的位置。
//
// 复现（fn / 非主账户 / meta_location=project）：会话文件按 resolveMetaPath 落在
// SharedMetaDir（<项目>/.mindfs），而 ExchangeLogAbsolutePath 用 MetaDir
// （<cfg>/users/<id>/meta/<rootID>）拼路径 —— 提示把 agent 指向一个永远不存在的文件，
// 提示文案又要求「读不到就报错停下」，于是每轮都停摆。
func TestExchangeLogAbsolutePathMatchesActualExchangeFile(t *testing.T) {
	project := t.TempDir()
	root := rootfs.NewRootInfo("CMAI", "CMAI", project)
	root.MetaLocation = rootfs.MetaLocationProject
	root.MetaRoot = filepath.Join(t.TempDir(), "meta")

	manager := NewManager(root)
	created, err := manager.Create(context.Background(), CreateInput{
		Type: TypeChat,
		Name: "Chat",
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	abs := manager.ExchangeLogAbsolutePath(created.Key)
	if abs == "" {
		t.Fatal("ExchangeLogAbsolutePath returned empty path")
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("switch hint path %q is not where the session store writes: %v", abs, err)
	}
}

// 块流的提示路径同样要落在真正落盘的位置，且「未见过的行数」按块行数算。
//
// 主日志自 S2d-2 起 content 恒空，正文只在块流里：路径对但指向主日志，agent
// 读到的是一串空 content，恢复不出上下文。
func TestBlocksLogPathAndUnseenCountMatchActualBlocksFile(t *testing.T) {
	project := t.TempDir()
	root := rootfs.NewRootInfo("CMAI", "CMAI", project)
	root.MetaLocation = rootfs.MetaLocationProject
	root.MetaRoot = filepath.Join(t.TempDir(), "meta")

	manager := NewManager(root)
	created, err := manager.Create(context.Background(), CreateInput{
		Type: TypeChat,
		Name: "Chat",
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	blocks := []Block{
		{Seq: 1, Pos: 0, Type: BlockTypeText, Text: "old"},
		{Seq: 2, Pos: 0, Type: BlockTypeText, Text: "seen"},
		{Seq: 3, Pos: 0, Type: BlockTypeText, Text: "unseen-1"},
		{Seq: 3, Pos: 1, Type: BlockTypeToolCall},
		{Seq: 4, Pos: 0, Type: BlockTypeText, Text: "unseen-2"},
	}
	if err := manager.AppendBlocks(context.Background(), created.Key, blocks); err != nil {
		t.Fatalf("AppendBlocks: %v", err)
	}

	abs := manager.BlocksLogAbsolutePath(created.Key)
	if abs == "" {
		t.Fatal("BlocksLogAbsolutePath returned empty path")
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("blocks path %q is not where the block stream is written: %v", abs, err)
	}

	got, err := manager.CountBlocksAfterSeq(created.Key, 2)
	if err != nil {
		t.Fatalf("CountBlocksAfterSeq: %v", err)
	}
	if want := 3; got != want {
		t.Fatalf("CountBlocksAfterSeq(after=2) = %d, want %d", got, want)
	}
}
