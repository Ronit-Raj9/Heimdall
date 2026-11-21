import "@/lib/i18n";

import type { OperateStatus } from "@/lib/types/operate";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { OperatePanels } from "./operateView";

// The job button needs the store; a plain button stands in for it.
vi.mock("@/components/jobs/jobRunButton", () => ({
	JobRunButton: ({ kind, label }: { kind: string; label: string }) => <button data-testid={`job-run-${kind}`}>{label}</button>,
}));

const local: OperateStatus = {
	hosted: false,
	kinds: ["operate.doctor", "gpu.check", "voice.check", "visual.capture", "visual.compare", "simulator.spread",
		"evidence.verify", "evidence.mirror", "evidence.archive", "evidence.backup", "evidence.restore", "k8s.status"],
	mirror: "folder",
	mirror_location: "/srv/mirror",
	gpu_mode: "cdi",
};

describe("OperatePanels", () => {
	it("offers doctor, GPU, Kubernetes and evidence jobs on a local server with a mirror", () => {
		const html = renderToStaticMarkup(<OperatePanels status={local} />);
		for (const kind of ["operate.doctor", "gpu.check", "k8s.status", "evidence.verify", "evidence.mirror", "evidence.archive", "evidence.backup"]) {
			expect(html).toContain(`data-testid="job-run-${kind}"`);
		}
		expect(html).toContain("/srv/mirror");
		// Restore waits for an experiment id.
		expect(html).not.toContain('data-testid="job-run-evidence.restore"');
		expect(html).toContain("Restore needs an experiment id.");
	});

	it("says how to configure a mirror instead of offering evidence buttons", () => {
		const html = renderToStaticMarkup(<OperatePanels status={{ ...local, mirror: "", mirror_location: undefined }} />);
		expect(html).toContain('data-testid="operate-no-mirror"');
		expect(html).toContain("storage.mirror_dir");
		expect(html).not.toContain('data-testid="job-run-evidence.verify"');
	});

	it("shows only the doctor on a hosted server", () => {
		const html = renderToStaticMarkup(<OperatePanels status={{ hosted: true, kinds: ["operate.doctor"], mirror: "" }} />);
		expect(html).toContain('data-testid="job-run-operate.doctor"');
		expect(html).not.toContain('data-testid="operate-evidence"');
		expect(html).not.toContain('data-testid="job-run-gpu.check"');
		expect(html).not.toContain('data-testid="operate-simulator"');
	});
});
