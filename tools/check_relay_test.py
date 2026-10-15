"""Offline pre-push checks against authored Git history; Go never executes."""
import contextlib
import importlib.machinery
import importlib.util
import io
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock


loader = importlib.machinery.SourceFileLoader("check_relay", str(Path(__file__).with_name("check-relay")))
spec = importlib.util.spec_from_loader(loader.name, loader)
check_relay = importlib.util.module_from_spec(spec)
loader.exec_module(check_relay)


class PushUpdates(unittest.TestCase):
    def test_only_distinct_nondeletion_main_candidates(self):
        first, second, zero = "a" * 40, "b" * 64, "0" * 40
        updates = [
            f"refs/heads/feature {first} refs/heads/feature {zero}\n",
            f"(delete) {zero} refs/heads/main {first}\n",
            f"refs/heads/release {first} refs/heads/main {zero}\n",
            f"refs/heads/main {first} refs/heads/main {zero}\n",
            f"refs/heads/main {second} refs/heads/main {first}\n",
        ]
        self.assertEqual(check_relay.push_revisions(updates), [first, second])

    def test_other_pushes_never_run_a_check(self):
        updates = io.StringIO(f"refs/tags/v1 {'a' * 40} refs/tags/v1 {'0' * 40}\n")
        with mock.patch.object(check_relay.sys, "stdin", updates), mock.patch.object(check_relay, "check_revision") as probe:
            self.assertEqual(check_relay.main(["--pre-push"]), 0)
        probe.assert_not_called()

    def test_pre_push_uses_candidate_not_head_and_propagates_failure(self):
        commit = "c" * 40
        updates = io.StringIO(f"refs/heads/release {commit} refs/heads/main {'a' * 40}\n")
        with mock.patch.object(check_relay.sys, "stdin", updates), mock.patch.object(check_relay, "check_revision", return_value=9) as probe:
            self.assertEqual(check_relay.main(["--pre-push"]), 9)
        probe.assert_called_once_with(commit)


class CommittedSnapshot(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="relay-check-fixture-")
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.git("init", "-q")
        for name in ("go.mod", "go.sum", "internal/relay/relay.go", "internal/netproto/wire.go", "unrelated.txt"):
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("authored fixture\n")
        self.missing = self.commit()
        self.probe = self.root / check_relay.PROBE
        self.probe.parent.mkdir(parents=True)
        self.probe.write_text("first committed probe\n")
        self.candidate = self.commit()
        self.probe.write_text("later committed probe\n")
        self.latest = self.commit()
        self.probe.write_text("dirty checkout probe\n")
        (self.root / "internal/relay/relay.go").write_text("dirty checkout relay\n")
        previous_directory = Path.cwd()
        os.chdir(self.root)
        self.addCleanup(os.chdir, previous_directory)

    def git(self, *args):
        return subprocess.check_output(["git", "-C", str(self.root), *args], stderr=subprocess.PIPE).decode().strip()

    def commit(self):
        self.git("add", ".")
        self.git("-c", "user.name=Relay fixture", "-c", "user.email=relay@example.invalid",
                 "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "commit", "-qm", "Authored fixture")
        return self.git("rev-parse", "HEAD")

    def test_head_and_selected_commit_ignore_dirty_and_unselected_files(self):
        real_run = subprocess.run
        for args, expected, commit, result in [
            ([], "later committed probe\n", self.latest, 0),
            (["--revision", self.candidate], "first committed probe\n", self.candidate, 7),
        ]:
            with self.subTest(args=args):
                snapshots = []

                def run(command, **kwargs):
                    if command[0] == "git":
                        return real_run(command, **kwargs)
                    self.assertEqual(command, ["go", "run", "./cmd/nanolathe-relay-check"])
                    snapshot = Path(kwargs["cwd"])
                    snapshots.append(snapshot)
                    self.assertEqual((snapshot / check_relay.PROBE).read_text(), expected)
                    self.assertEqual((snapshot / "internal/relay/relay.go").read_text(), "authored fixture\n")
                    self.assertFalse((snapshot / "unrelated.txt").exists())
                    self.assertEqual({p.name for p in snapshot.iterdir()}, {"go.mod", "go.sum", "cmd", "internal"})
                    self.assertEqual(kwargs["env"]["GOWORK"], "off")
                    self.assertEqual(kwargs["env"]["GOFLAGS"], "-trimpath -p=4")
                    self.assertEqual(kwargs["env"]["GOCACHE"], "/fixture-shared-go-cache")
                    return subprocess.CompletedProcess(command, result)

                output = io.StringIO()
                with mock.patch.object(check_relay.subprocess, "run", side_effect=run), contextlib.redirect_stdout(output), \
                        mock.patch.dict(os.environ, GOWORK="/dirty/go.work", GOFLAGS="-overlay=/dirty/overlay.json", GOCACHE="/fixture-shared-go-cache"):
                    self.assertEqual(check_relay.main(args), result)
                self.assertIn(commit, output.getvalue())
                self.assertEqual(len(snapshots), 1)
                self.assertFalse(snapshots[0].exists(), "temporary candidate survived the check")

    def test_old_candidate_without_probe_fails_clearly(self):
        real_run = subprocess.run

        def run(command, **kwargs):
            self.assertEqual(command[0], "git", "missing probe must fail before Go runs")
            return real_run(command, **kwargs)

        output = io.StringIO()
        with mock.patch.object(check_relay.subprocess, "run", side_effect=run), contextlib.redirect_stderr(output):
            self.assertEqual(check_relay.main(["--revision", self.missing]), 1)
        self.assertIn(f"candidate {self.missing} has no committed {check_relay.PROBE}", output.getvalue())


if __name__ == "__main__":
    unittest.main()
