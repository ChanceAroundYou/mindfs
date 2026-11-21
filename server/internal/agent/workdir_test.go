package agent

import (
	"path/filepath"
	"runtime"
	"testing"
)

// 生产 systemd 单元为了 10G 上传把 TMPDIR 指到 /var/tmp，而 /tmp 仍是系统的
// 临时盘（tmpfs）。只比较 os.TempDir() 会让 /tmp/** 下的隔离实例、探针与崩溃
// 注入目录被当成正式项目自动注册（见 docs/upstream-customizations.yaml G-NZ）。
func TestIsTemporaryWorkDirTreatsEverySystemTempRootAsTemporary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("固定临时根 /tmp、/var/tmp 只在类 Unix 平台有意义")
	}
	t.Setenv("TMPDIR", "/var/tmp")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	cases := []struct {
		name string
		path string
		want bool
	}{
		{"os.TempDir 之下", "/var/tmp/mindfs-claude/default", true},
		{"os.TempDir 本身", "/var/tmp", true},
		{"TMPDIR 之外的系统临时盘", "/tmp/mindfs-crash-sim/proj", true},
		{"/tmp 本身", "/tmp", true},
		{"XDG_RUNTIME_DIR 之下", "/run/user/1000/mindfs-probe", true},
		{"前缀相同但不是子目录", "/tmpfoo/project", false},
		{"普通项目目录", filepath.Join(string(filepath.Separator), "home", "mindfs", "project"), false},
		{"空路径", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsTemporaryWorkDir(tc.path); got != tc.want {
				t.Fatalf("IsTemporaryWorkDir(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TMPDIR 指到 /var/tmp 时，/var/tmp 之外的临时盘不能因为 TMPDIR 变了就失去豁免。
func TestIsTemporaryWorkDirKeepsWorkingWhenTempDirIsOutsideSlashTmp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("固定临时根 /tmp、/var/tmp 只在类 Unix 平台有意义")
	}
	t.Setenv("TMPDIR", "/var/tmp")
	t.Setenv("XDG_RUNTIME_DIR", "")

	if IsTemporaryWorkDir("/var/tmp/mindfs-iso/proj") != true {
		t.Fatal("os.TempDir() 之下的路径应判为临时目录")
	}
	if IsTemporaryWorkDir("/tmp/mindfs-iso/proj") != true {
		t.Fatal("/tmp 之下的路径应判为临时目录")
	}
	if IsTemporaryWorkDir("/tmp/mindfs-iso/proj/..") != true {
		t.Fatal("带 .. 的相对写法归一化后仍应判为临时目录")
	}
}
