import { useParams } from "@tanstack/react-router";
import SuiteView from "./views/suiteView";

// Composition only. The screen lives in views/.
export default function SuitePage() {
	const { suiteId } = useParams({ from: "/suites_/$suiteId" });
	return <SuiteView suiteId={suiteId} />;
}