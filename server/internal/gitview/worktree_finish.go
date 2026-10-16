package gitview

import (
	"errors"
	"fmt"
	"strings"
)

// worktree finish：把 linked worktree 里的活收尾掉所需的底层能力。
//
// 拆成独立文件而不是塞进 gitview.go：这个场景的每条命令都有「**必须**在主 checkout
// 里执行」或「**绝不能**在 worktree 里执行」的方向性约束，跟普通 status/diff 那些
// 「在哪儿跑都行」的读操作不是一类。放一起会让「哪些命令对位置敏感」看不出来。

// ErrMergeConflict 表示合并撞上冲突、仓库停在 MERGE_HEAD 未收尾的状态。
//
// 刻意让「冲突」和「合并失败」能被上层分开：前者要给用户人工处理步骤（哪边内容对、
// 怎么合、要不要拆成别的改法，机器判断不了），后者是别的原因。合成一个错误的话
// 上层只能一律回一句「合并失败」，用户还得自己去猜是哪种。
//
// 不是自动 abort 的理由：手工解决到一半的冲突取舍会连同 MERGE_MSG 一起丢掉。
var ErrMergeConflict = errors.New("merge conflict")

// ErrMergeUncommitted 表示仓库停在一个「冲突已解完、但合并还没提交」的中间态。
//
// 单独一个错误而不是塞进 ErrMergeConflict：这时 `git diff --diff-filter=U` 是空的，
// 没有冲突文件可列，报成冲突只会让前端渲染一个空清单加一句「去解冲突」—— 而用户
// 要做的其实是提交（或 abort）。两者的下一步完全不同，混在一起用户会反复点
// 这个按钮却永远成功不了。
var ErrMergeUncommitted = errors.New("merge resolved but not committed")

// ErrSourceBranchGone 表示要合并的分支在仓库里已经不存在了。
//
// 当成「已合入」而不是错误：git 自己不允许删掉未合并的分支（`branch -d` 会拒绝），
// 所以一个消失的分支要么是已经合过、要么是压根没有提交 —— 两种情况都不需要再合。
// 而**重复收尾必然走到这里**：第一次已经 `branch -d` 掉了，第二次如果在这里报错，
// 用户会看到一次成功的收尾第二次点却失败。
var ErrSourceBranchGone = errors.New("源分支已不存在（视为已合入）")

// ErrMergeDirty 表示主 checkout 有未提交改动，git 不敢替用户合并。
var ErrMergeDirty = errors.New("main checkout dirty")

// MainCheckoutDirtyError 表示主 checkout 有未提交改动，合并会覆盖它。
//
// 单独一个类型而不是塞进 error 字符串：那不是「收尾炸了」，是「你自己的活还没收」——
// 下一步是提交或暂存，不是重试。前端要按文件清单渲染，从字符串里解析文件名迟早会散。
type MainCheckoutDirtyError struct {
	Files []string
}

func (e *MainCheckoutDirtyError) Error() string {
	files := strings.Join(e.Files, ", ")
	if files == "" {
		files = "（见 git 输出）"
	}
	return "主 checkout 有未提交改动，先处理：" + files
}

func (e *MainCheckoutDirtyError) Unwrap() error { return ErrMergeDirty }

// MergeConflict 描述一次撞上冲突的合并，ConflictFiles 是 UU/AA 那些文件。
type MergeConflict struct {
	// ConflictFiles 是 unmerged 的路径。冲突可能由三方内容导致，未必每个都进这里，
	// 所以它只当「先看这几个」用，不是全集。
	ConflictFiles []string
	// Output 是 git 自己的原话（CONFLICT (content): Merge conflict in ...）。
	Output string
}

func (e MergeConflict) Error() string {
	files := strings.Join(e.ConflictFiles, ", ")
	if files == "" {
		files = "(见下方 git 输出)"
	}
	return fmt.Sprintf("%v: %s", ErrMergeConflict, files)
}

func (e MergeConflict) Unwrap() error { return ErrMergeConflict }
