#!/usr/bin/env python3
"""Pull mode with a LangGraph agent.

Heimdall owns the task's tools and runs them itself, so the graph here is the
model step of a tool-calling agent: a StateGraph over MessagesState whose
model node is ChatOpenAI with the turn's tools bound. When the model asks for
tools, the answer carries the tool calls; Heimdall runs them through the
task's tools (recorded in the evaluation) and the next turn brings their
results in turn.messages. When it answers in text, that is the reply.

    pip install langgraph langchain-openai
    HEIMDALL_URL=... HEIMDALL_TOKEN=... OPENAI_API_KEY=... python3 langgraph_agent.py

Your own LangGraph app keeps its graph: put its nodes before or after
"model" and keep tool execution with Heimdall, or answer in text only.
Env: HEIMDALL_AGENT (default support-bot), AGENT_MODEL, OPENAI_BASE_URL.
"""

import json
import os
import sys

try:
    from langchain_core.messages import SystemMessage, convert_to_messages
    from langchain_openai import ChatOpenAI
    from langgraph.graph import END, START, MessagesState, StateGraph
except ImportError:
    sys.exit("this example needs LangGraph: pip install langgraph langchain-openai")

try:
    import heimdall
except ImportError:  # running from the repository without installing heimdall-bench
    sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "python"))
    import heimdall

AGENT = os.environ.get("HEIMDALL_AGENT", "support-bot")
MODEL = os.environ.get("AGENT_MODEL", "gpt-5.4-mini")
SYSTEM = "You are a helpful support agent. Use the tools when they help, then answer briefly."


def build_graph(tools):
    model = ChatOpenAI(model=MODEL)
    if tools:
        model = model.bind_tools(tools)

    def call_model(state):
        return {"messages": [model.invoke([SystemMessage(SYSTEM)] + state["messages"])]}

    g = StateGraph(MessagesState)
    g.add_node("model", call_model)
    g.add_edge(START, "model")
    g.add_edge("model", END)
    return g.compile()


def solve(turn):
    graph = build_graph(turn.openai_tools())
    out = graph.invoke({"messages": convert_to_messages(turn.messages)})
    last = out["messages"][-1]
    calls = [heimdall.ToolCall(id=c.get("id") or "", name=c["name"], arguments=c.get("args") or {})
             for c in getattr(last, "tool_calls", None) or []]
    meta = getattr(last, "usage_metadata", None) or {}
    text = last.content if isinstance(last.content, str) else json.dumps(last.content)
    return heimdall.Answer(text=text, tool_calls=calls,
                           usage={"input_tokens": meta.get("input_tokens", 0), "output_tokens": meta.get("output_tokens", 0)})


if __name__ == "__main__":
    heimdall.serve(AGENT, solve, concurrency=int(os.environ.get("CONCURRENCY", "4")), log=lambda m: print(m, file=sys.stderr))
