import { getRouteApi } from "@tanstack/react-router";
import DeviceView from "./views/deviceView";

const route = getRouteApi("/device");

// Composition only. The screen lives in views/.
export default function DevicePage() {
	const { code } = route.useSearch();
	return <DeviceView initialCode={code} />;
}
