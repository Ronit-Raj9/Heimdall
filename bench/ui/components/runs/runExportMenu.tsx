import { Button } from "@/components/ui/button";
import { BENCH_API_BASE } from "@/lib/store/apis/fixtures";
import { ChevronDown, Download } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { runTraceUrl } from "./traceView.utils";

const ITEMS = [
  { format: "json", ext: "trace.json" },
  { format: "otlp", ext: "otlp.json" },
  { format: "jsonl", ext: "steps.jsonl" },
] as const;

/** The run page Export menu: the run's trace as JSON, OTLP JSON, or one step per line. */
export function RunExportMenu({
  runId,
  sealed,
}: {
  runId: string;
  sealed: boolean;
}) {
  const { t } = useTranslation("runTrace");
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const esc = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("mousedown", close);
    document.addEventListener("keydown", esc);
    return () => {
      document.removeEventListener("mousedown", close);
      document.removeEventListener("keydown", esc);
    };
  }, [open]);
  return (
    <div className="relative" ref={ref} data-testid="run-export">
      <Button
        variant="outline"
        size="sm"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        data-testid="run-export-button"
      >
        <Download aria-hidden="true" />
        {t("export.button")}
        <ChevronDown aria-hidden="true" />
      </Button>
      {open && (
        <ul
          role="menu"
          className="bg-popover absolute right-0 z-20 mt-1 flex w-64 flex-col rounded-xl border p-1 shadow-md"
        >
          {ITEMS.map(({ format, ext }) => {
            const disabled = format === "otlp" && !sealed;
            return (
              <li key={format} role="none">
                <a
                  role="menuitem"
                  href={
                    disabled
                      ? undefined
                      : runTraceUrl(BENCH_API_BASE, runId, format)
                  }
                  download={`${runId}.${ext}`}
                  aria-disabled={disabled}
                  onClick={() => setOpen(false)}
                  data-testid={`run-export-${format}`}
                  className={
                    disabled
                      ? "flex cursor-not-allowed flex-col rounded-lg px-3 py-2 opacity-50"
                      : "hover:bg-accent flex flex-col rounded-lg px-3 py-2"
                  }
                >
                  <span className="text-sm font-medium">
                    {t(`export.${format}`)}
                  </span>
                  <span className="text-muted-foreground text-xs">
                    {t(`export.${format}Note`)}
                  </span>
                </a>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
