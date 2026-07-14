import os
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
ARCHON = ROOT / ".archon"


class StratumAdapterContractTest(unittest.TestCase):
    def test_project_contract_contains_required_safety_settings(self):
        contract = (ARCHON / "project.yaml").read_text()
        self.assertIn("max_concurrent_tasks: 3", contract)
        self.assertIn("config/prod.yaml", contract)
        self.assertIn("AGENTS.md", contract)
        self.assertIn("stratum-e2e-development", contract)

    def test_project_commands_and_scripts_exist(self):
        for name in ("stratum-context", "stratum-implement", "stratum-review", "stratum-e2e"):
            self.assertTrue((ARCHON / "commands" / f"{name}.md").is_file(), name)
        for name in ("verify-short", "verify-full", "verify-e2e"):
            path = ARCHON / "scripts" / f"{name}.sh"
            self.assertTrue(path.is_file(), name)
            self.assertTrue(path.stat().st_mode & 0o111, name)

    def test_config_routes_claude_and_codex(self):
        config = (ARCHON / "config.yaml").read_text()
        self.assertIn("claudeBinaryPath: /home/yang/.local/bin/claude", config)
        self.assertIn("codexBinaryPath: /usr/local/bin/codex", config)

    def test_e2e_guard_refuses_missing_acceptance_contract(self):
        result = subprocess.run(
            [str(ARCHON / "scripts" / "verify-e2e.sh")],
            cwd=ROOT,
            text=True,
            capture_output=True,
        )
        self.assertNotEqual(0, result.returncode)
        self.assertIn("acceptance contract", result.stderr.lower())

    def test_e2e_guard_refuses_missing_feature_runner(self):
        with tempfile.NamedTemporaryFile(mode="w") as acceptance:
            acceptance.write("The approved user journey succeeds in the local test environment.\n")
            acceptance.flush()
            env = os.environ.copy()
            env["STRATUM_E2E_ACCEPTANCE_FILE"] = acceptance.name
            result = subprocess.run(
                [str(ARCHON / "scripts" / "verify-e2e.sh")],
                cwd=ROOT,
                env=env,
                text=True,
                capture_output=True,
            )
        self.assertNotEqual(0, result.returncode)
        self.assertIn("e2e runner missing", result.stderr.lower())


if __name__ == "__main__":
    unittest.main()
