#!/usr/bin/env python3
"""Check that community regressions catch deliberate permission/data-loss defects."""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile


ROOT = Path(__file__).resolve().parents[1]
MUTATIONS = [
    ("active account guard", "internal/forum/community_store.go", "if suspended {", "if false && suspended {", "TestSuspensionRevokesAccessAndRestorePreservesContributions"),
    ("stale reader guard", "internal/forum/access.go", "WHERE id = r.id AND suspended = 1", "WHERE id = r.id AND suspended = 1 AND 0", "TestSuspensionRevokesAccessAndRestorePreservesContributions"),
    ("session revocation", "internal/forum/community_store.go", "DELETE FROM sessions WHERE user_id = ?", "DELETE FROM sessions WHERE user_id = ? AND 0", "TestSuspensionRevokesAccessAndRestorePreservesContributions"),
    ("reset revocation", "internal/forum/community_store.go", "DELETE FROM auth_tokens WHERE user_id = ?", "DELETE FROM auth_tokens WHERE user_id = ? AND 0", "TestSuspensionRevokesAccessAndRestorePreservesContributions"),
    ("invitation revocation", "internal/forum/community_store.go", "WHERE created_by = ?", "WHERE created_by = ? AND 0", "TestSuspensionRevokesAccessAndRestorePreservesContributions"),
    ("owner authorization", "internal/forum/board_lifecycle.go", 'if role != "owner" {', 'if false && role != "owner" {', "TestMemberActionsRequireOwnerConfirmationAndCurrentRevision"),
    ("member confirmation", "internal/forum/community_store.go", "if confirmation != name {", "if false && confirmation != name {", "TestMemberActionsRequireOwnerConfirmationAndCurrentRevision"),
    ("message revision", "internal/forum/community_store.go", "if p.Revision != revision || replaceTitle && p.Number != 1 {", "if replaceTitle && p.Number != 1 {", "TestRemovalConfirmationCannotDeleteNewEditsOrReplaceReplyTitle"),
    ("attachment erasure", "internal/forum/community_store.go", "DELETE FROM attachments WHERE post_id = ?", "DELETE FROM attachments WHERE post_id = ? AND 0", "TestRemovalErasesTextImagesAndSearchButKeepsReplyContext"),
    ("removed message schema", "internal/forum/migrations/013_community_care.sql", "AND (removed = 0 OR body = 'This message was removed by a site owner.')", "", "TestCommunitySchemaUpgradeAndConstraints"),
]


def check():
    env = dict(os.environ, GOPROXY="off", GOSUMDB="off", GOTOOLCHAIN="local")
    env["GOFLAGS"] = (env.get("GOFLAGS", "") + " -buildvcs=false").strip()
    with tempfile.TemporaryDirectory(prefix="witmoot-community-mutations-") as temporary:
        checkout = Path(temporary) / "source"
        shutil.copytree(ROOT, checkout, ignore=shutil.ignore_patterns(".git", "node_modules", "bin", "dist", "*.db", "*.db-wal", "*.db-shm", "__pycache__"))
        tests = "^(" + "|".join(sorted({m[4] for m in MUTATIONS})) + ")$"
        baseline = subprocess.run(["go", "test", "./internal/forum", "-count=1", "-run", tests], cwd=checkout, env=env, text=True, capture_output=True, timeout=120)
        if baseline.returncode:
            raise RuntimeError("baseline failed:\n" + baseline.stdout + baseline.stderr)
        for name, file, before, after, regression in MUTATIONS:
            path = checkout / file
            original = path.read_text()
            if original.count(before) != 1:
                raise RuntimeError(f"mutation anchor changed: {name}")
            path.write_text(original.replace(before, after))
            try:
                result = subprocess.run(["go", "test", "./internal/forum", "-count=1", "-run", "^" + regression + "$"], cwd=checkout, env=env, text=True, capture_output=True, timeout=120)
                if result.returncode == 0 or "--- FAIL: " + regression not in result.stdout:
                    raise RuntimeError(f"mutation was not caught by its regression: {name}\n{result.stdout}{result.stderr}")
                print(f"PASS: regression rejects broken {name}", flush=True)
            finally:
                path.write_text(original)


if __name__ == "__main__":
    check()
