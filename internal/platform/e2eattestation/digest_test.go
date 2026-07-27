package e2eattestation

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDigestPathsUsesSortedPathNULContentNULRecords(t *testing.T) {
	contents := map[string][]byte{"a.txt": []byte("alpha"), "z.txt": []byte("omega")}
	first, err := digestPaths([]string{"z.txt", "a.txt"}, func(path string) ([]byte, error) { return contents[path], nil })
	require.NoError(t, err)
	second, err := digestPaths([]string{"a.txt", "z.txt"}, func(path string) ([]byte, error) { return contents[path], nil })
	require.NoError(t, err)
	require.Equal(t, first, second)
	_, err = digestPaths([]string{"missing"}, func(path string) ([]byte, error) { return nil, fmt.Errorf("missing %s", path) })
	require.ErrorContains(t, err, "missing")
}

func TestLocalSourceDigestCoversTrackedAndUntrackedSource(t *testing.T) {
	root := initDigestRepository(t)
	first, err := LocalSourceDigest(root)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("one"), 0o600))
	second, err := LocalSourceDigest(root)
	require.NoError(t, err)
	require.NotEqual(t, first, second)
	require.NoError(t, os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("two"), 0o600))
	third, err := LocalSourceDigest(root)
	require.NoError(t, err)
	require.NotEqual(t, second, third)
}

func TestLocalSourceDigestExcludesAttestationOutputAndIgnoredFiles(t *testing.T) {
	root := initDigestRepository(t)
	before, err := LocalSourceDigest(root)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test/e2e/attestations"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "test/e2e/attestations/report.json"), []byte("runtime"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ignored.log"), []byte("runtime"), 0o600))
	after, err := LocalSourceDigest(root)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestCommittedSourceDigestIsStableAcrossWorkingTreeMutation(t *testing.T) {
	root := initDigestRepository(t)
	before, err := CommittedSourceDigest(root, "HEAD")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("changed"), 0o600))
	after, err := CommittedSourceDigest(root, "HEAD")
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func initDigestRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.log\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("tracked"), 0o600))
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-qm", "init"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		require.NoError(t, command.Run())
	}
	return root
}
