import json
import subprocess
import unittest
from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[1]


class LocalInfraContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        rendered = subprocess.run(
            ["docker", "compose", "config", "--format", "json"],
            cwd=ROOT,
            check=True,
            capture_output=True,
            text=True,
        )
        cls.compose = json.loads(rendered.stdout)
        cls.services = cls.compose["services"]
        cls.resources = {}
        for manifest in sorted((ROOT / "infra" / "k8s").glob("*.yaml")):
            for resource in yaml.safe_load_all(manifest.read_text()):
                if resource:
                    cls.resources[(resource["kind"], resource["metadata"]["name"])] = resource

    def test_compose_runs_application_and_local_dependencies(self):
        self.assertTrue(
            {
                "postgres",
                "qdrant",
                "localstack",
                "migrate",
                "api",
                "market-worker",
                "agent",
            }.issubset(self.services)
        )
        self.assertIn("postgres-data", self.compose["volumes"])
        self.assertIn("qdrant-data", self.compose["volumes"])
        self.assertIn("postgres", self.services["api"]["environment"]["DATABASE_URL"])
        self.assertIn("qdrant", self.services["agent"]["environment"]["QDRANT_URL"])
        self.assertIn("localstack", self.services["agent"]["environment"]["AWS_ENDPOINT_URL"])
        self.assertEqual(self.services["market-worker"]["command"], ["/usr/local/bin/market-worker"])
        self.assertIn("backend/db/migrations", self.services["migrate"]["volumes"][0]["source"])
        self.assertEqual(
            self.services["api"]["depends_on"]["migrate"]["condition"],
            "service_completed_successfully",
        )

    def test_kubernetes_has_local_app_deployments_and_probes(self):
        for name in ("api", "market-worker", "agent"):
            deployment = self.resources[("Deployment", name)]
            pod = deployment["spec"]["template"]["spec"]
            container = pod["containers"][0]
            if name == "market-worker":
                self.assertEqual(container["command"], ["/usr/local/bin/market-worker"])
            self.assertEqual(deployment["spec"]["selector"]["matchLabels"], {"app": name})
            self.assertEqual(
                [next(iter(source)) for source in container["envFrom"]],
                ["secretRef", "configMapRef"],
            )
            self.assertTrue(
                {"configMapRef", "secretRef"}.issubset(
                    {
                        key
                        for source in container["envFrom"]
                        for key in source
                    }
                )
            )
            self.assertIn("livenessProbe", container)
            self.assertIn("startupProbe", container)
        self.assertIn(("ConfigMap", "fin-agent-config"), self.resources)
        self.assertNotIn(("Secret", "fin-agent-secrets"), self.resources)
        self.assertNotIn(("Ingress", "fin-agent"), self.resources)
        config = self.resources[("ConfigMap", "fin-agent-config")]["data"]
        self.assertNotIn("GEMINI_API_KEY", config)
        self.assertNotIn("OPENROUTER_API_KEY", config)
        for name in ("api", "agent"):
            service = self.resources[("Service", name)]
            self.assertEqual(service["spec"]["selector"], {"app": name})
            self.assertEqual(service["spec"]["ports"][0]["targetPort"], "http")
        localstack = self.resources[("Service", "localstack")]
        self.assertEqual(localstack["spec"]["type"], "ExternalName")
        self.assertEqual(localstack["spec"]["externalName"], "host.docker.internal")
        migration_job = self.resources[("Job", "fin-agent-migrations")]
        self.assertEqual(migration_job["spec"]["template"]["spec"]["restartPolicy"], "OnFailure")
        self.assertIn(("StatefulSet", "postgres"), self.resources)


if __name__ == "__main__":
    unittest.main()
