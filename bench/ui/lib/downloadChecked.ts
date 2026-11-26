import i18n from "@/lib/i18n";

/**
 * Downloads url as filename only when the server answers with the file. A refusal (a 409 with a
 * JSON message, a 404, a proxy error) is returned as the sentence to show, and nothing is saved, so
 * the browser never stores an error page under the file's name. Null means the file was saved.
 */
export async function downloadChecked(url: string, filename: string): Promise<string | null> {
	let res: Response;
	try {
		res = await fetch(url, { credentials: "include" });
	} catch {
		return i18n.t("errors.offline", { ns: "w4m0" });
	}
	if (!res.ok) {
		let message = "";
		try {
			const body = (await res.json()) as { error?: { message?: string } };
			message = body?.error?.message ?? "";
		} catch {
			// Not JSON: a proxy's page. The status says what happened.
		}
		if (message) return message;
		if (res.status === 413) return i18n.t("errors.tooLarge", { ns: "w4m0" });
		return i18n.t("errors.unreadable", { ns: "w4m0", status: res.status });
	}
	const blob = await res.blob();
	const href = URL.createObjectURL(blob);
	const a = document.createElement("a");
	a.href = href;
	a.download = filename;
	document.body.appendChild(a);
	a.click();
	a.remove();
	URL.revokeObjectURL(href);
	return null;
}