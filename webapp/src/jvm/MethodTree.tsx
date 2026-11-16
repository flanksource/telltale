import { ExecutionTree, ObjectGraph } from "@flanksource/clicky-ui/data";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import { executionNodes, graphNode, objectNode, type MethodEvent, type MethodNode } from "./results";

function valueNode(value: unknown, label: string) {
  return value && typeof value === "object" && !Array.isArray(value) && "label" in value ? graphNode(value as Record<string, unknown>) : objectNode(value, label);
}

export function MethodTree({ events, agent }: { events: MethodEvent[]; agent?: string }) {
  const { renderLink } = useRouter();
  return <ExecutionTree<MethodNode> roots={executionNodes(events)} showControls empty="No method entries in this snapshot"
    renderDetail={node => <div className="ml-4 space-y-2 border-l pl-3">
      {agent && node.className && renderLink({ to: `/jvm/${encodeURIComponent(agent)}/jvm.classes.source?${new URLSearchParams({ class: node.className, method: node.label + (node.detail?.descriptor ?? "") })}`, children: "Inspect method source", className: "text-xs underline" })}
      {node.args?.map((value, index) => <section key={index} className="space-y-1"><h4 className="text-xs text-muted-foreground">Argument {index}</h4><ObjectGraph roots={[valueNode(value, `Argument ${index}`)]} showOptionsMenu showFullscreenControl /></section>)}
    </div>}
    renderAfterChildren={node => <div className="ml-4 space-y-2 border-l pl-3">
      {node.error && <p role="alert" className="whitespace-pre-wrap break-words text-sm text-destructive">{node.error}</p>}
      {node.value !== undefined && <ObjectGraph roots={[valueNode(node.value, "Return value")]} showOptionsMenu showFullscreenControl />}
    </div>}
  />;
}
