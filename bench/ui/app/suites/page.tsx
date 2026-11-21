import CatalogView from "@/app/catalog/views/catalogView";
import AddSuiteView from "@/app/suites_/add/views/addSuiteView";
import { RouteTabs } from "@/components/shell";
import "@/lib/types/hubsI18n";
import { useTranslation } from "react-i18next";
import SuitesView from "./views/suitesView";

// Composition only. Benchmarks: My benchmarks (default), Catalog (the old /catalog) and Import
// (the old /imports and /suites/add) as tabs, ?tab= in the URL.
export default function SuitesPage() {
	const { t } = useTranslation("hubs");
	return (
		<RouteTabs
			tabs={[
				{ id: "mine", label: t("suites.mine") },
				{ id: "catalog", label: t("suites.catalog") },
				{ id: "import", label: t("suites.import") },
			]}
			defaultTab="mine"
			label={t("suites.label")}
			testId="suites-hub-tabs"
			className="mx-auto w-full max-w-[1200px]"
		>
			{(active) => (active === "catalog" ? <CatalogView /> : active === "import" ? <AddSuiteView /> : <SuitesView />)}
		</RouteTabs>
	);
}
