#!/usr/bin/env python3
"""Check that sandbox regressions detect policy and fail-open defects."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
MUTATIONS = [
    ("environment filtering", "internal/sandbox/sandbox.go", 'strings.HasPrefix(key, s.Prefix) && key != s.Prefix+"DATA_DIR"', 'key != s.Prefix+"DATA_DIR"', "./internal/sandbox", "TestServicePolicyKeepsSecretsOutOfArguments"),
    ("writable data directory", "internal/sandbox/sandbox.go", 'args = append(args, "--bind", data, data)', 'args = append(args, "--ro-bind", data, data)', "./internal/sandbox", "TestServicePolicyKeepsSecretsOutOfArguments"),
    ("broad filesystem mounts", "internal/sandbox/sandbox.go", 'name == "/" ||', 'false ||', "./internal/sandbox", "TestWritableDirectoriesRejectBroadAndAdversarialPaths"),
    ("merged-usr symlinks", "internal/sandbox/sandbox.go", "info.Mode()&os.ModeSymlink != 0 && within(real, bound)", "info.Mode()&os.ModeSymlink != 0 && within(real, nil)", "./internal/sandbox", "TestRuntimeMountsRecreateMergedUsrSymlinks"),
    ("symlink containment", "internal/sandbox/sandbox.go", "info.Mode()&os.ModeSymlink != 0 && within(real, bound)", "info.Mode()&os.ModeSymlink != 0", "./internal/sandbox", "TestRuntimeMountsRecreateMergedUsrSymlinks"),
    ("check command", "internal/sandbox/sandbox.go", 'append(append(append([]string{}, args...), "--"), command...)', 'append(append([]string{}, args...), "--", "/usr/bin/true")', "./internal/sandbox", "TestCheckRunsTheGivenCommand"),
    ("custom certificate bundle", "internal/sandbox/sandbox.go", 'if custom != "" {', 'if false && custom != "" {', "./internal/sandbox", "TestServicePolicyHonoursCustomCertificateBundle"),
    ("certificate bundle validation", "internal/sandbox/sandbox.go", "if info, err := os.Stat(custom); err != nil || !info.Mode().IsRegular() {", "if info, err := os.Stat(custom); err != nil && info.Mode().IsRegular() {", "./internal/sandbox", "TestCustomCertificateBundleMustBeAnExistingFile"),
]


def check():
    env = dict(os.environ, GOPROXY="off", GOSUMDB="off", GOTOOLCHAIN="local", COMFYWARE_SANDBOX_TEST="1")
    env["GOFLAGS"] = (env.get("GOFLAGS", "") + " -buildvcs=false").strip()
    with tempfile.TemporaryDirectory(prefix="comfyware-sandbox-mutations-") as temporary:
        checkout = Path(temporary) / "source"
        shutil.copytree(ROOT, checkout, ignore=shutil.ignore_patterns(".git", "node_modules", "bin", "dist", ".release", "*.db", "*.db-wal", "*.db-shm", "__pycache__"))
        for name, file, before, after, package, regression in MUTATIONS:
            command = ["go", "test", package, "-count=1", "-run", "^" + regression + "$", "-timeout=30s"]
            baseline = subprocess.run(command, cwd=checkout, env=env, text=True, capture_output=True, timeout=90)
            if baseline.returncode:
                raise RuntimeError("baseline failed:\n" + baseline.stdout + baseline.stderr)
            path = checkout / file
            original = path.read_text()
            if original.count(before) != 1:
                raise RuntimeError("mutation anchor changed: " + name)
            path.write_text(original.replace(before, after))
            try:
                result = subprocess.run(command, cwd=checkout, env=env, text=True, capture_output=True, timeout=90)
                if result.returncode == 0 or "--- FAIL: " + regression not in result.stdout:
                    raise RuntimeError("mutation was not caught: " + name + "\n" + result.stdout + result.stderr)
                print("PASS: regression rejects broken " + name, flush=True)
            finally:
                path.write_text(original)


if __name__ == "__main__":
    check()
