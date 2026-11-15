import type { Trends, TrendsResponse } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";

/** The trends fixture the fixture mode serves (public/fixtures/trends.json), for tests only. */
export function trendsFixture(): TrendsResponse {
	const file = path.resolve(__dirname, "../../public/fixtures/trends.json");
	return JSON.parse(fs.readFileSync(file, "utf8")) as TrendsResponse;
}

/** A copy of data with only the first n points of its longest lineage and no other lineage. */
export function truncated(data: Trends, n: number): Trends {
	const longest = [...data.lineages].sort((a, b) => b.points.length - a.points.length)[0];
	const lineage = { ...longest, points: longest.points.slice(0, n) };
	return { ...data, lineages: n > 0 ? [lineage] : [], points: n };
}