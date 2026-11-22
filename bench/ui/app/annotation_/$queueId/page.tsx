import { useParams } from "@tanstack/react-router";
import QueueView from "./views/queueView";

// Composition only. The screen lives in views/.
export default function QueuePage() {
	const { queueId } = useParams({ from: "/annotation_/$queueId" });
	return <QueueView queueId={queueId} />;
}