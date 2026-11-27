import CatalogView from "@/app/catalog/views/catalogView";
import AddSuiteView from "@/app/suites_/add/views/addSuiteView";
import { RouteTabs } from "@/components/shell";
import "@/lib/types/hubsI18n";
import { useTranslation } from "react-i18next";
import SuitesView, { SuitesPageHeader } from "./views/suitesView";

// Composition only. Benchmarks: My benchmarks (default), Catalog (the old /catalog) and Import
// (the old /imports) as tabs, ?tab= in the URL. /suites/add is the Add benchmark page (Z20).
export default function SuitesPage() {
  const { t } = useTranslation("hubs");
  // X-42: one header (h1, subtitle, Add menu) above the tabs; the tab views carry no h1 of their own.
  return (
    <div className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-4">
      <SuitesPageHeader
        tabs={
          <RouteTabs
            tabs={[
              { id: "mine", label: t("suites.mine") },
              { id: "catalog", label: t("suites.catalog") },
              { id: "import", label: t("suites.import") },
            ]}
            defaultTab="mine"
            label={t("suites.label")}
            testId="suites-hub-tabs"
          >
            {(active) =>
              active === "catalog" ? (
                <CatalogView />
              ) : active === "import" ? (
                <AddSuiteView />
              ) : (
                <SuitesView />
              )
            }
          </RouteTabs>
        }
      />
    </div>
  );
}
