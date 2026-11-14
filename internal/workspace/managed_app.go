package workspace

import "context"

// InitializeManagedAppRepository only snapshots the caller's newly created,
// controller-owned seed. All commands use the existing trusted Git executable,
// clean environment, disabled hooks/protocols and bounded output/time handling.
func InitializeManagedAppRepository(ctx context.Context, repo string) error {
	for _, args := range [][]string{{"-c", "init.templateDir=", "init", "-q"}, {"add", "--all"}, {"-c", "user.name=Foundry", "-c", "user.email=foundry@localhost", "-c", "commit.gpgsign=false", "commit", "-qm", "Managed browser application seed"}} {
		if _, err := gitCommand(ctx, repo, 4096, args...); err != nil {
			return err
		}
	}
	return nil
}
