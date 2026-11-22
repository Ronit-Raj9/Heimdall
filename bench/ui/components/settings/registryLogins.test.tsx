import "@/lib/i18n";

import { registryFormProblem } from "@/lib/types/registryCredentials";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { RegistryLoginsTable } from "./registryLogins";

const rows = [
	{ id: "k1", kind: "ghcr" as const, host: "ghcr.io", username: "octo", secret_name: "GHCR_TOKEN", created_at: "", secret_found: true },
	{ id: "k2", kind: "ecr" as const, host: "1.dkr.ecr.us-east-1.amazonaws.com", username: "AKIA", secret_name: "ECR_KEY", created_at: "", secret_found: false },
];

describe("RegistryLoginsTable", () => {
	it("shows hosts, users and secret names, flags a missing secret", () => {
		const html = renderToStaticMarkup(<RegistryLoginsTable logins={rows} canWrite />);
		expect(html).toContain("ghcr.io");
		expect(html).toContain("GHCR_TOKEN");
		expect(html).toContain('data-testid="registry-secret-missing-k2"');
		expect(html).toContain('data-testid="registry-delete-k1"');
	});
	it("hides remove from non admins and offers a next step when empty", () => {
		expect(renderToStaticMarkup(<RegistryLoginsTable logins={rows} canWrite={false} />)).not.toContain("registry-delete-k1");
		expect(renderToStaticMarkup(<RegistryLoginsTable logins={[]} canWrite={false} />)).toContain("Ask an org admin");
	});
	it("checks the form", () => {
		expect(registryFormProblem("ecr", "", "AKIA", "K")).toContain("host");
		expect(registryFormProblem("gcr", "europe-docker.pkg.dev", "", "K")).toBe("");
		expect(registryFormProblem("ghcr", "", "octo", "")).toContain("secret");
	});
});
