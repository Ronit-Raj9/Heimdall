import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SourceBox } from "./sourceBox";

const q = vi.hoisted(() => ({ secrets: [] as { name: string }[] }));
vi.mock("@/lib/store/apis/secretsApi", async (orig) => ({
	...(await orig<typeof import("@/lib/store/apis/secretsApi")>()),
	useListSecretsQuery: () => ({ data: { secrets: q.secrets } }),
}));

beforeEach(() => {
	q.secrets = [];
});

describe("SourceBox access token (M6-55)", () => {
	it("offers the org's saved secrets as a select", () => {
		q.secrets = [{ name: "hf-token" }];
		const html = render(<SourceBox busy={false} onSubmit={() => undefined} />);
		expect(html).toMatch(/<button[^>]*role="combobox"[^>]*data-testid="onboard-connection"|data-testid="onboard-connection"[^>]*role="combobox"/);
	});
	it("keeps a typed secret id when none is saved", () => {
		const html = render(<SourceBox busy={false} onSubmit={() => undefined} />);
		expect(html).toMatch(/<input[^>]*data-testid="onboard-connection"/);
	});
});
