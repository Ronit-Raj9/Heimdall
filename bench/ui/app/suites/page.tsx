import CatalogView from "@/app/catalog/views/catalogView";
import { RouteTabs } from "@/components/shell";
import "@/lib/types/hubsI18n";
import { useTranslation } from "react-i18next";
import SuitesView, { SuitesPageHeader } from "./views/suitesView";

// Composition only. Benchmarks: My benchmarks (default) and Catalog (the old /catalog) as tabs,
// ?tab= in the URL. The Import tab is gone (decision 5): importers sit in the catalog with their
// command line text, and ?tab=import redirects to the catalog (layout.tsx). /suites/add is the Add
// benchmark page (Z20).
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
            ]}
            defaultTab="mine"
            label={t("suites.label")}
            testId="suites-hub-tabs"
          >
            {(active) =>
              active === "catalog" ? (
                <CatalogView />
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
