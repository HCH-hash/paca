import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { StatusAssignmentRulesPanel } from "@/components/projects/automation/status-assignment-rules-panel";
import { usePermissions } from "@/hooks/use-permissions";
import { useProjectPermissions } from "@/hooks/use-project-permissions";
import {
	projectMembersQueryOptions,
	projectQueryOptions,
	taskStatusesQueryOptions,
} from "@/lib/project-api";
import { statusRulesQueryOptions } from "@/lib/status-rule-api";

export const Route = createFileRoute(
	"/_authenticated/projects/$projectId/automation/status-rules",
)({
	loader: async ({ context: { queryClient }, params: { projectId } }) => {
		await Promise.all([
			queryClient.ensureQueryData(projectQueryOptions(projectId)),
			queryClient.ensureQueryData(taskStatusesQueryOptions(projectId)),
			queryClient.ensureQueryData(projectMembersQueryOptions(projectId)),
			queryClient.ensureQueryData(statusRulesQueryOptions(projectId)),
		]);
	},
	component: StatusRulesPage,
});

function StatusRulesPage() {
	const { t } = useTranslation("projects");
	const { projectId } = Route.useParams();
	const { data: project } = useQuery(projectQueryOptions(projectId));
	const { hasPermission } = usePermissions();
	const { hasProjectPermission } = useProjectPermissions(projectId);
	const canWrite =
		hasPermission("tasks.write") || hasProjectPermission("tasks.write");

	return (
		<div className="flex flex-col">
			<div className="relative overflow-hidden border-b border-border/50">
				<div
					className="pointer-events-none absolute inset-0 opacity-50"
					style={{
						backgroundImage:
							"radial-gradient(circle, color-mix(in oklch, var(--color-primary) 12%, transparent) 1px, transparent 1px)",
						backgroundSize: "20px 20px",
						maskImage:
							"radial-gradient(ellipse 70% 100% at 0% 0%, black 20%, transparent 70%)",
					}}
				/>
				<div className="relative px-6 py-8">
					<h1 className="font-[Syne] text-2xl font-bold tracking-tight">
						{t("automation.statusRulesPage.title")}
					</h1>
					<p className="mt-1 text-sm text-muted-foreground">
						{project?.name} · {t("automation.statusRulesPage.subtitle")}
					</p>
				</div>
			</div>

			<div className="p-6">
				<StatusAssignmentRulesPanel projectId={projectId} canWrite={canWrite} />
			</div>
		</div>
	);
}
