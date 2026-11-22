import { createFileRoute } from "@tanstack/react-router";
import QueuePage from "./page";

// One labelling queue. "annotation_" keeps it out of /annotation, which renders no Outlet; the
// path is still /annotation/$queueId.
export const Route = createFileRoute("/annotation_/$queueId")({
	component: QueuePage,
});