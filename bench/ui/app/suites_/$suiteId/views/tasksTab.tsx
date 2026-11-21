import { EmptyNote, Section } from "@/components/runs/screenStates";
import type { HealthState } from "@/components/suites/suiteHealth.utils";
import { SuiteTaskFilters } from "@/components/suites/suiteTaskFilters";
import { SuiteTaskTable } from "@/components/suites/suiteTaskTable";
import {
  ADMISSION_STATES,
  DEFAULT_TASK_SORT,
  SORT_DIRS,
  filterOptions,
  filterTasks,
  nextTaskSort,
  parseTaskSort,
  sortTasks,
  type TaskFilters,
  type TaskSortKey,
} from "@/components/suites/suites.utils";
import { TaskDetailPanel } from "@/components/suites/taskDetailPanel";
import type { SuiteSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { parseAsString, parseAsStringLiteral, useQueryStates } from "nuqs";
import { useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { useTranslation } from "react-i18next";

/** URL state of the task table: filters, sort and the open task. */
export const TASK_QUERY = {
  kind: parseAsString,
  difficulty: parseAsString,
  cap: parseAsString,
  status: parseAsStringLiteral(ADMISSION_STATES),
  sort: parseAsString,
  dir: parseAsStringLiteral(SORT_DIRS),
  task: parseAsString,
};

/** Tasks tab: the filterable task table, the task in ?task= open beside it. */
export function SuiteTasksTab({
  suite,
  health,
}: {
  suite: SuiteSummary;
  health: HealthState;
}) {
  const { t } = useTranslation("bench");
  const { t: tu } = useTranslation("suitesUx");
  const [q, setQ] = useQueryStates(TASK_QUERY);
  const filters: TaskFilters = {
    kind: q.kind,
    difficulty: q.difficulty,
    capability: q.cap,
    status: q.status,
  };
  const sort = parseTaskSort(q.sort, q.dir);
  const options = useMemo(() => filterOptions(suite.tasks), [suite.tasks]);
  const rows = useMemo(
    () =>
      sortTasks(
        filterTasks(suite.tasks, {
          kind: q.kind,
          difficulty: q.difficulty,
          capability: q.cap,
          status: q.status,
        }),
        parseTaskSort(q.sort, q.dir),
      ),
    [suite.tasks, q.kind, q.difficulty, q.cap, q.status, q.sort, q.dir],
  );
  // Expert columns (kind, skills tested, license, hidden checks) stay behind a toggle.
  const [moreColumns, setMoreColumns] = useState(false);
  const openTask =
    q.task && suite.tasks.some((task) => task.id === q.task) ? q.task : null;

  const onSort = (key: TaskSortKey) => {
    const next = nextTaskSort(sort, key);
    const isDefault =
      next.key === DEFAULT_TASK_SORT.key && next.dir === DEFAULT_TASK_SORT.dir;
    void setQ({
      sort: isDefault ? null : next.key,
      dir: isDefault ? null : next.dir,
    });
  };

  return (
    <div
      className={cn(
        "grid min-w-0 grid-cols-1 gap-4",
        openTask && "2xl:grid-cols-[minmax(0,1fr)_minmax(0,30rem)]",
      )}
    >
      <Section
        title={tu("tasksTitle")}
        description={tu("tasksDescription")}
        testId="suites-tasks"
        className="min-w-0"
      >
        <SuiteTaskFilters
          filters={filters}
          options={options}
          onChange={(next) =>
            void setQ({
              kind: next.kind,
              difficulty: next.difficulty,
              cap: next.capability,
              status: next.status,
            })
          }
          onReset={() =>
            void setQ({ kind: null, difficulty: null, cap: null, status: null })
          }
        />
        {suite.tasks.length === 0 ? (
          <EmptyNote testId="suites-tasks-empty">
            {t("suites.tasks.empty")}
          </EmptyNote>
        ) : rows.length === 0 ? (
          <EmptyNote testId="suites-tasks-no-match">
            {t("suites.tasks.noMatch")}
          </EmptyNote>
        ) : (
          <>
            <div className="flex justify-end">
              <Button
                size="sm"
                variant="ghost"
                aria-pressed={moreColumns}
                onClick={() => setMoreColumns((v) => !v)}
                data-testid="suites-tasks-more-columns"
              >
                {moreColumns ? tu("fewerColumns") : tu("moreColumns")}
              </Button>
            </div>
            <SuiteTaskTable
              suiteId={suite.id}
              rows={rows}
              sort={sort}
              onSort={onSort}
              selected={openTask}
              onOpen={(id) => void setQ({ task: id === openTask ? null : id })}
              health={health}
              moreColumns={moreColumns}
            />
            {rows.length < suite.tasks.length && (
              <p
                className="text-muted-foreground text-xs"
                data-testid="suites-tasks-showing"
              >
                {t("suites.tasks.showing", {
                  shown: rows.length,
                  total: suite.tasks.length,
                })}
              </p>
            )}
          </>
        )}
      </Section>
      {openTask && (
        <div className="min-w-0 2xl:sticky 2xl:top-4 2xl:self-start">
          <TaskDetailPanel
            suiteId={suite.id}
            suiteVersion={suite.version}
            taskId={openTask}
            suiteOutput={suite.output}
            suiteArtifacts={suite.artifacts}
            onClose={() => void setQ({ task: null })}
            health={health}
            quarantine={
              suite.tasks.find((task) => task.id === openTask)?.quarantine ??
              null
            }
          />
        </div>
      )}
    </div>
  );
}
