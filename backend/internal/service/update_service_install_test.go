//go:build unit

package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type updateInstallClient struct {
	updateServiceGitHubClientStub
	failDownload bool
	latestErr    error
	latestCalls  int
	recentCalls  int
}

func (c *updateInstallClient) FetchLatestRelease(ctx context.Context, repo string) (*GitHubRelease, error) {
	c.latestCalls++
	if c.latestErr != nil {
		return nil, c.latestErr
	}
	return c.updateServiceGitHubClientStub.FetchLatestRelease(ctx, repo)
}

func (c *updateInstallClient) FetchRecentReleases(ctx context.Context, repo string, limit int) ([]*GitHubRelease, error) {
	c.recentCalls++
	return c.updateServiceGitHubClientStub.FetchRecentReleases(ctx, repo, limit)
}

func (c *updateInstallClient) DownloadFile(_ context.Context, _, dest string, _ int64) error {
	if c.failDownload {
		return errors.New("download unavailable")
	}
	return os.WriteFile(dest, []byte("downloaded release"), 0600)
}

func TestUpdateServicePendingRestartPrecedesReleaseLookup(t *testing.T) {
	for _, scenario := range []string{"update-already-current", "update-offline", "version-rollback-offline"} {
		t.Run(scenario, func(t *testing.T) {
			client := &updateInstallClient{}
			client.release = &GitHubRelease{TagName: "v2.0.0"}
			client.recentErr = errors.New("GitHub unavailable")
			if scenario == "update-offline" {
				client.latestErr = errors.New("GitHub unavailable")
			}
			svc := NewUpdateService(&updateServiceCacheStub{}, client, "2.0.0", "release")
			// The running version remains unchanged after an install or rollback.
			svc.restartRequired = true

			var err error
			if scenario == "version-rollback-offline" {
				err = svc.RollbackToVersion(context.Background(), "1.0.0")
			} else {
				err = svc.PerformUpdate(context.Background())
			}
			require.ErrorIs(t, err, ErrSystemRestartRequired)
			require.Equal(t, "SYSTEM_RESTART_REQUIRED", infraerrors.Reason(err))
			require.Zero(t, client.latestCalls, "pending restart must not query the latest release")
			require.Zero(t, client.recentCalls, "pending restart must not query rollback releases")
		})
	}
}

func TestUpdateServiceRequiresRestartBeforeNextMutation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-place executable replacement requires Unix rename semantics")
	}
	const childEnv = "SUB2API_UPDATE_INSTALL_TEST"
	if scenario := os.Getenv(childEnv); scenario != "" {
		runUpdateInstallScenario(t, scenario)
		return
	}

	for _, scenario := range []string{"update-update", "update-rollback", "rollback-update", "rollback-rollback", "failed-update-retry"} {
		t.Run(scenario, func(t *testing.T) {
			// Never rename the parent test runner: only the disposable child copy.
			executable, err := os.Executable()
			require.NoError(t, err)
			source, err := os.Open(executable)
			require.NoError(t, err)
			defer func() { _ = source.Close() }()
			childPath := filepath.Join(t.TempDir(), "sub2api")
			child, err := os.OpenFile(childPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
			require.NoError(t, err)
			_, copyErr := io.Copy(child, source)
			require.NoError(t, child.Close())
			require.NoError(t, copyErr)
			cmd := exec.Command(childPath, "-test.run=^TestUpdateServiceRequiresRestartBeforeNextMutation$", "-test.timeout=30s")
			cmd.Env = append(os.Environ(), childEnv+"="+scenario)
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", output)
		})
	}
}

func runUpdateInstallScenario(t *testing.T, scenario string) {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	executable, err = filepath.EvalSymlinks(executable)
	require.NoError(t, err)
	client := &updateInstallClient{}
	svc := NewUpdateService(&updateServiceCacheStub{}, client, "1.0.0", "release")
	client.release = &GitHubRelease{
		TagName: "v2.0.0",
		Assets: []GitHubAsset{{
			Name:               svc.getArchiveName(),
			BrowserDownloadURL: "https://github.com/example/releases/" + svc.getArchiveName(),
		}},
	}
	update := func() error { return svc.PerformUpdate(context.Background()) }
	if scenario == "failed-update-retry" {
		client.failDownload = true
		require.ErrorContains(t, update(), "download unavailable")
		client.failDownload = false
	}
	if scenario == "rollback-update" || scenario == "rollback-rollback" {
		require.NoError(t, os.WriteFile(executable+".backup", []byte("previous release"), 0755))
		require.NoError(t, svc.Rollback())
	} else {
		require.NoError(t, update())
	}

	before := updateInstallFileHashes(t, executable)
	if scenario == "update-rollback" || scenario == "rollback-rollback" {
		err = svc.Rollback()
	} else {
		err = update()
	}
	require.Error(t, err, "a successful install must require restart before any further mutation")
	require.Equal(t, "SYSTEM_RESTART_REQUIRED", infraerrors.Reason(err))
	require.Equal(t, before, updateInstallFileHashes(t, executable), "rejected mutations must preserve the installed binary and backup")
	require.NoFileExists(t, executable+".backup.backup")
}

func updateInstallFileHashes(t *testing.T, executable string) map[string][sha256.Size]byte {
	t.Helper()
	hashes := make(map[string][sha256.Size]byte)
	for _, path := range []string{executable, executable + ".backup"} {
		file, err := os.Open(path)
		if os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err)
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		require.NoError(t, file.Close())
		require.NoError(t, copyErr)
		var digest [sha256.Size]byte
		copy(digest[:], hash.Sum(nil))
		hashes[filepath.Base(path)] = digest
	}
	return hashes
}
