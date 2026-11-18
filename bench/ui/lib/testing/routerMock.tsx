// A stand-in for @tanstack/react-router in component tests, which render to static markup with
// no router. Use it with:
//   vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
// Links render as plain anchors (params filled in, search as a query string), Navigate renders a
// marker element, and the location is whatever routerState holds.
import type { ReactNode } from "react";
import { vi } from "vitest";

export const routerState = {
	location: { pathname: "/verdict", href: "/verdict", search: {} as Record<string, unknown> },
};

export const navigateSpy = vi.fn();

function hrefOf(to: string, params?: Record<string, string>, search?: Record<string, unknown>): string {
	let path = to;
	for (const [key, value] of Object.entries(params ?? {})) path = path.replace(`$${key}`, encodeURIComponent(value));
	const entries = Object.entries(search ?? {}).filter(([, value]) => value !== undefined && value !== null);
	const qs = new URLSearchParams(entries.map(([key, value]) => [key, String(value)])).toString();
	return qs ? `${path}?${qs}` : path;
}

interface LinkProps {
	to: string;
	params?: Record<string, string>;
	search?: Record<string, unknown>;
	children?: ReactNode;
	activeProps?: unknown;
	activeOptions?: unknown;
	[key: string]: unknown;
}

export const routerMock = {
	Link: ({ to, params, search, children, activeProps: _a, activeOptions: _o, ...rest }: LinkProps) => (
		<a href={hrefOf(to, params, search)} {...(rest as Record<string, unknown>)}>
			{children}
		</a>
	),
	Navigate: ({ to, search }: { to: string; search?: Record<string, unknown> }) => (
		<span data-testid="navigate" data-to={hrefOf(to, undefined, search)} />
	),
	useNavigate: () => navigateSpy,
	useRouterState: ({ select }: { select: (state: typeof routerState) => unknown }) => select(routerState),
	useParams: () => ({}),
	useSearch: () => routerState.location.search,
};