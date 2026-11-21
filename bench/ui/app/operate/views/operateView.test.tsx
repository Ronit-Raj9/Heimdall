import "@/lib/i18n";

import type { OperateStatus } from "@/lib/types/operate";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { BackupRows, OperatePanels } from "./operateView";

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
		expect(html).toContain("To restore, enter a comparison id.");
	});

	it("says how to configure a mirror instead of offering evidence buttons", () => {
		const html = renderToStaticMarkup(<OperatePanels status={{ ...local, mirror: "", mirror_location: undefined }} />);
		expect(html).toContain('data-testid="operate-no-mirror"');
		expect(html).toContain("storage.mirror_dir");
		expect(html).not.toContain('data-testid="job-run-evidence.verify"');
	});

	it("offers doctor, verify and backup on a hosted server without a mirror, with a next step", () => {
		const hosted: OperateStatus = {
			hosted: true,
			kinds: ["operate.doctor", "evidence.verify", "evidence.archive", "evidence.backup", "evidence.restore"],
			mirror: "",
		};
		const html = renderToStaticMarkup(<OperatePanels status={hosted} backups={<BackupRows backups={[]} />} />);
		for (const kind of ["operate.doctor", "evidence.verify", "evidence.backup"]) {
			expect(html).toContain(`data-testid="job-run-${kind}"`);
		}
		expect(html).not.toContain('data-testid="job-run-evidence.archive"');
		expect(html).not.toContain('data-testid="job-run-evidence.mirror"');
		expect(html).not.toContain('data-testid="job-run-gpu.check"');
		expect(html).not.toContain('data-testid="operate-simulator"');
		expect(html).toContain('data-testid="operate-no-mirror"');
		expect(html).toContain('data-testid="operate-workers-link"');
		expect(html).toContain('data-testid="operate-backups-empty"');
	});

	it("offers archive and restore on a hosted server with a mirror, without its location", () => {
		const hosted: OperateStatus = {
			hosted: true,
			kinds: ["operate.doctor", "evidence.verify", "evidence.archive", "evidence.backup", "evidence.restore"],
			mirror: "s3",
		};
		const html = renderToStaticMarkup(<OperatePanels status={hosted} />);
		expect(html).toContain('data-testid="job-run-evidence.archive"');
		expect(html).toContain("To restore, enter a comparison id.");
		expect(html).not.toContain("s3://");
	});

	it("lists backups with download links", () => {
		const html = renderToStaticMarkup(
			<BackupRows backups={[{ name: "evidence-org_a-20261121-100000.zip", size: 2048, mod_time: "2026-11-21T10:00:00Z" }]} />,
		);
		expect(html).toContain('href="/api/bench/operate/backups/evidence-org_a-20261121-100000.zip"');
		expect(html).toContain("2.0 KiB");
	});
});
