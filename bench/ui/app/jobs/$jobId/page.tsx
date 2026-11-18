import { getRouteApi } from "@tanstack/react-router";
import JobView from "./views/jobView";

const route = getRouteApi("/jobs/$jobId");

// Composition only. The screen lives in views/.
export default function JobPage() {
	const { jobId } = route.useParams();
	return <JobView jobId={jobId} />;
}