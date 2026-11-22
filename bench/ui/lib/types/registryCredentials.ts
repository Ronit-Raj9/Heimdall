/** Private image registry logins (handlers/registrycreds.go). No password is ever sent back. */
export type RegistryKind = "dockerhub" | "ghcr" | "ecr" | "gcr" | "acr" | "other";

export interface RegistryCredential {
	id: string;
	kind: RegistryKind;
	host: string;
	username: string;
	secret_name: string;
	region?: string;
	created_by?: string;
	created_at: string;
	/** False when the named secret is missing under Settings > Secrets. */
	secret_found: boolean;
}

export interface RegistryCredentialList {
	credentials: RegistryCredential[];
	kinds: RegistryKind[];
	max: number;
}

export interface RegistryCredentialCreate {
	kind: RegistryKind;
	host?: string;
	username?: string;
	secret_name: string;
	region?: string;
}

export interface RegistryKindInfo {
	label: string;
	/** Fixed host, or the placeholder of the host field. */
	host?: string;
	hostPlaceholder?: string;
	userLabel: string;
	/** The user name is filled by the server (Google). */
	fixedUser?: string;
	secretHint: string;
}

export const REGISTRY_KINDS: Record<RegistryKind, RegistryKindInfo> = {
	dockerhub: { label: "Docker Hub", host: "docker.io", userLabel: "User name", secretHint: "a Docker Hub access token" },
	ghcr: { label: "GitHub (GHCR)", host: "ghcr.io", userLabel: "GitHub user name", secretHint: "a token with read:packages" },
	ecr: {
		label: "Amazon ECR",
		hostPlaceholder: "123456789012.dkr.ecr.us-east-1.amazonaws.com",
		userLabel: "Access key id",
		secretHint: "the secret access key; Heimdall gets a short-lived pull token for each run",
	},
	gcr: {
		label: "Google (GCR, Artifact Registry)",
		hostPlaceholder: "europe-docker.pkg.dev",
		userLabel: "User name",
		fixedUser: "_json_key",
		secretHint: "the service account JSON key",
	},
	acr: { label: "Azure (ACR)", hostPlaceholder: "myregistry.azurecr.io", userLabel: "Service principal id", secretHint: "the service principal password" },
	other: { label: "Other registry", hostPlaceholder: "registry.example.com", userLabel: "User name", secretHint: "the password or token" },
};

/** Why the form cannot be sent, or "". */
export function registryFormProblem(kind: RegistryKind, host: string, username: string, secretName: string): string {
	const info = REGISTRY_KINDS[kind];
	if (!info.host && !host.trim()) return "Enter the registry host.";
	if (!info.fixedUser && !username.trim()) return `Enter the ${info.userLabel.toLowerCase()}.`;
	if (!secretName) return "Pick the secret that holds the password.";
	return "";
}
