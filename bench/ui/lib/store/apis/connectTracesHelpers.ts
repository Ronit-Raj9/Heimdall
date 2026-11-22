// Pure helpers of the Connect from traces page (no store imports, so tests use them directly).

/** The setup text with the token filled in once one was made. */
export function withToken(text: string, token: string | null): string {
	return token ? text.split("$HEIMDALL_INGEST_TOKEN").join(token) : text;
}

/** The request the page posts: the draft's, with the person's edits. Null while it cannot be made. */
export function connectRequest(
	draft: { template?: string; params?: Record<string, string>; model?: { provider: string; model: string } },
	edits: { id: string; url: string; authSecret: string },
): { template: string; id: string; params: Record<string, string>; model?: { provider: string; model: string } } | null {
	const template = draft.template || "http-url";
	const params: Record<string, string> = { ...(draft.params ?? {}) };
	const id = edits.id.trim();
	if (!/^[a-z0-9][a-z0-9._-]{0,62}$/.test(id)) return null;
	const urlKey = template === "a2a-card" ? "card_url" : "url";
	if (template !== "wrap-app") {
		const url = edits.url.trim();
		if (!/^https?:\/\/[^\s]+$/.test(url)) return null;
		params[urlKey] = url;
	}
	const secret = edits.authSecret.trim();
	if (secret && template !== "a2a-card" && template !== "wrap-app") {
		if (!/^[A-Za-z_][A-Za-z0-9_]{0,127}$/.test(secret)) return null;
		params[template === "openai-endpoint" ? "api_key_secret" : "auth_secret"] = secret;
	}
	if (template === "wrap-app" && !params.command) return null;
	return { template, id, params, ...(draft.model?.model ? { model: draft.model } : {}) };
}
