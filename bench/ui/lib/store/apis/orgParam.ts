import { getAuthState } from "./session";

/**
 * Query parameter naming the tab's organization on a GET the browser opens directly (a download,
 * an image, an event stream), where no header can be sent (wave 4, organization per tab). The
 * server honours it on GET and HEAD only; writes name their organization in the header.
 */
export const ORG_QUERY_PARAM = "heimdall_org";

/** url with the tab's organization added in hosted mode; unchanged in local mode or before the organization is known. */
export function withOrgParam(url: string): string {
	const { mode, org } = getAuthState();
	if (mode !== "hosted" || !org) return url;
	return `${url}${url.includes("?") ? "&" : "?"}${ORG_QUERY_PARAM}=${encodeURIComponent(org)}`;
}
