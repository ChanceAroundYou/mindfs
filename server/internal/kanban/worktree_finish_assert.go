package kanban

import (
	"context"
	"fmt"

	"mindfs/server/internal/gitview"
)

func (s *Service) assertWorktreeMatches(ctx context.Context, mainDir, worktreePath, branch string) error {
	binding, err := gitview.InspectWorktree(ctx, mainDir, worktreePath)
	if err != nil {
		return err
	}
	if !binding.Registered {
		return nil
	}
	actual := binding.BranchName()
	if actual == "" {
		return fmt.Errorf("%s 现在是 detached HEAD（%s），不再是 %s 分支 —— 先确认那里有没有要保留的活再收尾",
			worktreePath, gitview.ShortSHA(binding.Head), branch)
	}
	if branch != "" && actual != branch {
		return fmt.Errorf("%s 现在挂在 %s 上，不是任务记的 %s —— 拆它会连别人的活一起拆掉，先人工确认",
			worktreePath, actual, branch)
	}
	return nil
}
