import type { ClusterDetailResponse, ClusterListResponse } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";

// Tests only. The synthetic payloads fixture mode serves for GET /clusters and GET
// /clusters/{signature} (public/fixtures), read once so tests and fixture mode share one copy.
// Run r_fmw1z7ouim is in the grid fixture, so its link to the run page can be checked.

const FIXTURE_DIR = path.resolve(__dirname, "../../public/fixtures");
const read = <T>(file: string): T => JSON.parse(fs.readFileSync(path.join(FIXTURE_DIR, file), "utf8")) as T;

export const CLUSTER_LIST_FIXTURE = read<ClusterListResponse>("clusters.json");

/** The detail of the third cluster, the tool error, whose error text carries markup and a URL. */
export const CLUSTER_DETAIL_FIXTURE = read<ClusterDetailResponse>("clusters.c_99aabbccddeeff00.json");