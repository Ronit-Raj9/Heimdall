import "@/lib/i18n";

import type { ImageRequest } from "@/lib/types/sharedpool";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AgentImageApproval, ImageRequestsCard, isBuiltinImage, requestFor, requestProblem } from "./imageRequests";

const s = vi.hoisted(() => ({ pool: {} as Record<string, unknown>, list: {} as Record<string, unknown> }));
vi.mock("@/lib/store/apis/sharedpoolApi", () => ({
	useGetPoolStatusQuery: () => s.pool,
	useListImageRequestsQuery: () => s.list,
	useRequestImageMutation: () => [vi.fn(), { isLoading: false }],
}));
vi.mock("@/lib/store/apis/benchApi", () => ({ getBenchErrorMessage: () => "err" }));

const pinned = "ghcr.io/org/agent@sha256:" + "a".repeat(64);
const req = (over: Partial<ImageRequest>): ImageRequest => ({ id: "r1", org_id: "o", image: pinned, size_bytes: 1, status: "pending", created_at: "", ...over });

beforeEach(() => {
	s.pool = { isLoading: false, data: { shared_pool_configured: true, kill_switch: false, limits: { allow_custom_images: false }, message: "" } };
	s.list = { data: { images: [req({}), req({ id: "r2", image: "x@sha256:" + "b".repeat(64), status: "rejected", decision: "too big" })] } };
});

describe("image request helpers", () => {
	it("knows built-in images, finds a request and checks the form", () => {
		expect(isBuiltinImage("bench/coding-core-base:1")).toBe(true);
		expect(isBuiltinImage(pinned)).toBe(false);
		expect(requestFor([req({})], ` ${pinned} `)?.id).toBe("r1");
		expect(requestProblem("python:3.12", "10")).toContain("Pin the image by digest");
		expect(requestProblem(pinned, "")).toContain("size in MiB");
		expect(requestProblem(pinned, "850")).toBe("");
	});
});

describe("ImageRequestsCard", () => {
	it("lists the org's requests with status and admin note, and the form", () => {
		const html = renderToStaticMarkup(<ImageRequestsCard initialImage={pinned} />);
		expect(html).toContain('data-testid="image-requests"');
		expect(html).toContain("Waiting for approval");
		expect(html).toContain("Not approved");
		expect(html).toContain("Admin note: too big");
		expect(html).toContain('data-testid="image-requests-off"');
		expect(html).toContain(`value="${pinned}"`);
	});

	it("is hidden without shared workers", () => {
		s.pool = { isLoading: false, data: { shared_pool_configured: false } };
		expect(renderToStaticMarkup(<ImageRequestsCard />)).toBe("");
	});
});

describe("AgentImageApproval", () => {
	it("shows the status of a requested image and a request link otherwise", () => {
		expect(renderToStaticMarkup(<AgentImageApproval image={pinned} />)).toContain("Waiting for approval");
		const html = renderToStaticMarkup(<AgentImageApproval image={"other@sha256:" + "c".repeat(64)} />);
		expect(html).toContain("Needs approval");
		expect(html).toContain('data-testid="agent-image-request-link"');
		expect(renderToStaticMarkup(<AgentImageApproval image="bench/agent-codex:1" />)).toBe("");
	});
});
