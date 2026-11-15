// Review fixtures for component tests, read from the same synthetic files fixture mode serves,
// so the tests follow the Go contract key lists those files are checked against.
import type { ReviewQueueResponse, RunResponse, RunReviewsResponse } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";

const FIXTURE_DIR = path.resolve(__dirname, "../../public/fixtures");

function read<T>(file: string): T {
	return JSON.parse(fs.readFileSync(path.join(FIXTURE_DIR, file), "utf8")) as T;
}

/** The queue: r_fmw1z7ouim (false completion, overruled to pass) and r_pww2l7yame (flagged). */
export const queueFixture = read<ReviewQueueResponse>("experiments.reviews.queue.json");
/** Reviews of r_fmw1z7ouim: one pass review in effect. */
export const passedRunReviews = read<RunReviewsResponse>("runs.r_fmw1z7ouim.reviews.json");
/** Reviews of r_pww2l7yame: one flag. */
export const flaggedRunReviews = read<RunReviewsResponse>("runs.r_pww2l7yame.reviews.json");
/** The run detail of r_fmw1z7ouim. */
export const runFixture = read<RunResponse>("runs.r_fmw1z7ouim.json");