import "@/lib/i18n";

import type { HardwareRecord } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { HardwarePanel } from "./hardwarePanel";

const gtx = {
	index: 0,
	uuid: "GPU-9434bd05",
	name: "NVIDIA GeForce GTX 1650",
	memory_mb: 4096,
	driver: "615.78.08",
	cuda_version: "13.4",
	compute_cap: "7.5",
};

describe("HardwarePanel", () => {
	it("renders nothing for a run without a GPU", () => {
		expect(renderToStaticMarkup(<HardwarePanel arm="A" />)).toBe("");
		expect(renderToStaticMarkup(<HardwarePanel arm="A" hardware={{ gpus: [] }} />)).toBe("");
	});

	it("shows the GPU and the sampler summary", () => {
		const hw: HardwareRecord = {
			gpus: [gtx],
			mode: "cdi",
			devices: ["nvidia.com/gpu=GPU-9434bd05"],
			sampler: {
				samples: 42,
				interval_ms: 500,
				agent_seconds: 21,
				gpu_seconds: 21,
				baseline_mb: 52,
				peak_used_mb: 1052,
				peak_vram_mb: 1000,
				mean_util_pct: 37.4,
				sessions: 1,
			},
		};
		const html = renderToStaticMarkup(<HardwarePanel arm="A" hardware={hw} />);
		expect(html).toContain("NVIDIA GeForce GTX 1650");
		expect(html).toContain("compute 7.5");
		expect(html).toContain("CUDA 13.4");
		expect(html).toContain('data-testid="gpu-sampler-A"');
		expect(html).toContain("1000 MiB above 52 MiB baseline");
		expect(html).toContain("Mean utilization 37%");
		expect(html).not.toContain("gpu-sampler-error-A");
	});

	it("says when there are no samples instead of showing zeros", () => {
		const hw: HardwareRecord = {
			gpus: [gtx],
			sampler: {
				samples: 0,
				interval_ms: 500,
				agent_seconds: 0,
				gpu_seconds: 0,
				baseline_mb: 0,
				peak_used_mb: 0,
				peak_vram_mb: 0,
				mean_util_pct: 0,
				sessions: 1,
				error: "nvidia-smi not found",
			},
		};
		const html = renderToStaticMarkup(<HardwarePanel arm="B" hardware={hw} />);
		expect(html).toContain('data-testid="gpu-no-sampler-B"');
		expect(html).not.toContain("Mean utilization");
		expect(html).toContain("nvidia-smi not found");
	});
});