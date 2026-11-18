import type {
	CalibrationItemResponse,
	CalibrationLabelRequest,
	CalibrationLabelsResponse,
	CalibrationStatusResponse,
} from "@/lib/types/bench";
import type { FetchArgs } from "@reduxjs/toolkit/query/react";
import { benchApi } from "./benchApi";

/**
 * W8 judge calibration (docs/judge-calibration.md), injected into the bench API so the shared
 * endpoint list stays untouched. Reads take no token; adding a label is a write and carries the
 * write token like every bench mutation. Running the judge costs money and stays on the command
 * line (`judge calibrate`), so the page never starts it.
 */
export const calibrationApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchCalibration"] }).injectEndpoints({
	endpoints: (builder) => ({
		getCalibration: builder.query<CalibrationStatusResponse, string>({
			query: (suiteId) => calibrationPath(suiteId),
			providesTags: (_result, _error, suiteId) => [{ type: "BenchCalibration", id: suiteId }],
		}),
		getCalibrationLabels: builder.query<CalibrationLabelsResponse, string>({
			query: (suiteId) => `${calibrationPath(suiteId)}/labels`,
			providesTags: (_result, _error, suiteId) => [{ type: "BenchCalibration", id: `${suiteId}/labels` }],
		}),
		addCalibrationLabel: builder.mutation<CalibrationItemResponse, { suiteId: string; body: CalibrationLabelRequest }>({
			query: ({ suiteId, body }) => calibrationLabelRequest(suiteId, body),
			invalidatesTags: (_result, _error, { suiteId }) => [
				{ type: "BenchCalibration", id: suiteId },
				{ type: "BenchCalibration", id: `${suiteId}/labels` },
			],
		}),
	}),
});

/** A suite's calibration path, with the id escaped as one path segment. */
export function calibrationPath(suiteId: string): string {
	return `/suites/${encodeURIComponent(suiteId)}/calibration`;
}

/** POST /suites/{id}/calibration/labels with the label as the JSON body; an empty note is left out. */
export function calibrationLabelRequest(suiteId: string, body: CalibrationLabelRequest): FetchArgs {
	const clean: CalibrationLabelRequest = { task: body.task.trim(), answer: body.answer, label: body.label, labeller: body.labeller.trim() };
	if (body.note && body.note.trim() !== "") clean.note = body.note.trim();
	if (body.ref && body.ref.trim() !== "") clean.ref = body.ref.trim();
	return { url: `${calibrationPath(suiteId)}/labels`, method: "POST", body: clean };
}

export const { useGetCalibrationQuery, useGetCalibrationLabelsQuery, useAddCalibrationLabelMutation } = calibrationApi;