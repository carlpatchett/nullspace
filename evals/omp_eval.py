#!/usr/bin/env python3
"""A/B evaluation driven through omp — real agent runs comparing search with vs without null-space.

Simulates realistic, day-to-day engineering workflows:
1. Symbol Investigation: "Explain how <Symbol> is implemented and used in this codebase, citing files and architecture."
2. Feature Verification / Negative Lookups: "Does this codebase implement <AbsentFeature>? If so, explain how; if not, confirm absence."

Baseline arm: a copy of the target codebase, NO null-space anywhere.
Augmented arm: an identical copy whose project ``.omp/mcp.json`` registers
null-space against that copy. Both arms run ``omp -p --mode json`` with the
same model, same prompt. Metrics come from omp's usage telemetry (input/output tokens and cost in USD),
tool-execution event stream, and null-space's OpenTelemetry JSONL spans for the augmented arm.

Usage:
  python3 evals/omp_eval.py --repo https://github.com/fastapi/fastapi --symbol OAuth2PasswordBearer
  python3 evals/omp_eval.py --repo https://github.com/expressjs/express --symbol DistributedCassandraSessionStore --negative-check
  python3 evals/omp_eval.py --fixture messy --symbol ExponentialBackoffRetryStrategy
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
from collections import Counter
from contextlib import contextmanager
from datetime import UTC, datetime
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
BINARY_PATH = REPO_ROOT / "bin" / "null-space"
GROUND_TRUTH_PATH = REPO_ROOT / "evals" / "ground_truth.json"
RESULTS_DIR = REPO_ROOT / "evals" / "results"

DEFAULT_MODEL = "google-antigravity/gemini-3.7-flash:medium"


def generate_task_prompt(symbol: str, is_negative: bool = False) -> tuple[str, str]:
    """Generate realistic day-to-day engineering prompts for baseline and augmented arms."""
    if is_negative:
        base_prompt = (
            f"An engineer asked: 'Does this codebase implement or use `{symbol}`? "
            f"If so, explain where it is located and how it works. If not present in the repository, "
            f"clearly verify and confirm that it does not exist.' "
            f"Do NOT modify any files; this is analysis only. Cite the exact files checked."
        )
        aug_prompt = (
            f"An engineer asked: 'Does this codebase implement or use `{symbol}`? "
            f"If so, explain where it is located and how it works. If not present in the repository, "
            f"clearly verify and confirm that it does not exist.' "
            f"Do NOT modify any files; this is analysis only.\n\n"
            f"CRITICAL INSTRUCTION: You MUST use the `mcp__null_space_prune_search_space` tool first to mathematically "
            f"prove which directories do or do not contain `{symbol}` before reading files or running broad greps."
        )
    else:
        base_prompt = (
            f"Can you explain the usage and implementation of `{symbol}` in this codebase? "
            f"Cite where it is defined, how it is used, and what its role is in the architecture. "
            f"Do NOT modify any files; this is analysis only."
        )
        aug_prompt = (
            f"Can you explain the usage and implementation of `{symbol}` in this codebase? "
            f"Cite where it is defined, how it is used, and what its role is in the architecture. "
            f"Do NOT modify any files; this is analysis only.\n\n"
            f"CRITICAL INSTRUCTION: You MUST use the `mcp__null_space_prune_search_space` tool first to prune non-matching "
            f"directories before reading files or running full-text greps. Rely on this tool's candidate directories."
        )
    return base_prompt, aug_prompt


def ensure_binary() -> Path:
    BINARY_PATH.parent.mkdir(parents=True, exist_ok=True)
    if not BINARY_PATH.exists():
        print(f"Building null-space binary at {BINARY_PATH}...", file=sys.stderr)
        subprocess.run(
            ["go", "build", "-o", str(BINARY_PATH), "./cmd/null-space"],
            cwd=str(REPO_ROOT),
            check=True,
        )
    return BINARY_PATH


def prepare_fixture_copies(base: Path, source_dir: Path) -> tuple[Path, Path, Path]:
    """Two identical fixture copies; the augmented one registers null-space."""
    baseline = base / "baseline_repo"
    augmented = base / "augmented_repo"
    shutil.copytree(source_dir, baseline)
    shutil.copytree(source_dir, augmented)

    trace_path = base / "trace_augmented.jsonl"
    mcp_config = {
        "$schema": (
            "https://raw.githubusercontent.com/can1357/oh-my-pi/main/"
            "packages/coding-agent/src/config/mcp-schema.json"
        ),
        "mcpServers": {
            "null-space": {
                "command": str(BINARY_PATH),
                "args": ["--root", str(augmented), "--trace", str(trace_path), "serve"],
                "env": {
                    "NULL_SPACE_ROOT": str(augmented),
                    "NULL_SPACE_TRACE_PATH": str(trace_path),
                },
            }
        },
    }
    (augmented / ".omp").mkdir(exist_ok=True)
    (augmented / ".omp" / "mcp.json").write_text(json.dumps(mcp_config, indent=2))
    return baseline, augmented, trace_path


@contextmanager
def isolated_user_mcp(home: Path | None = None):
    """Move the user-level omp mcp.json aside; restore on exit."""
    path = (home or Path.home()) / ".omp" / "agent" / "mcp.json"
    backup = path.with_name("mcp.json.null-space-eval-swap")
    moved = False
    if path.exists():
        path.rename(backup)
        moved = True
    try:
        yield moved
    finally:
        if moved and backup.exists():
            backup.rename(path)


def parse_events(raw: str) -> dict:
    usage_total = {"input": 0, "output": 0, "cost": 0.0}
    llm_calls = 0
    tools: Counter = Counter()
    final_text = ""
    saw_agent_end = False

    for line in raw.splitlines():
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue

        etype = event.get("type")
        if etype == "message_end":
            message = event.get("message", {})
            if message.get("role") == "assistant":
                usage = message.get("usage") or {}
                usage_total["input"] += usage.get("input", 0)
                usage_total["output"] += usage.get("output", 0)
                usage_total["cost"] += (usage.get("cost") or {}).get("total", 0.0)
                llm_calls += 1
                final_text = "".join(
                    part.get("text", "")
                    for part in message.get("content", [])
                    if isinstance(part, dict) and part.get("type") == "text"
                )
        elif etype == "tool_execution_start":
            name = (
                event.get("tool")
                or event.get("toolName")
                or event.get("name")
                or "unknown"
            )
            tools[str(name)] += 1
        elif etype == "agent_end":
            saw_agent_end = True

    return {
        "usage": usage_total,
        "llm_calls": llm_calls,
        "tool_calls": dict(sorted(tools.items())),
        "final_text": final_text,
        "completed": saw_agent_end,
    }


def _clean_env() -> dict:
    return {
        key: value
        for key, value in os.environ.items()
        if not key.startswith(("OMP_", "PI_"))
    }


def run_arm(
    omp_bin: str,
    cwd: Path,
    model: str,
    max_time: str,
    task: str,
    timeout_padding: int = 120,
) -> dict:
    match = re.fullmatch(r"(\d+)([smh]?)", max_time.strip())
    if not match:
        raise ValueError(f"bad --max-time: {max_time}")
    unit = {"": 1, "s": 1, "m": 60, "h": 3600}[match.group(2)]
    budget = int(match.group(1)) * unit

    command = [
        omp_bin,
        "-p",
        "--no-session",
        "--no-title",
        "--mode",
        "json",
        "--model",
        model,
        "--cwd",
        str(cwd),
        "--no-skills",
        "--no-lsp",
        "--auto-approve",
        "--max-time",
        max_time,
        task,
    ]

    started = time.perf_counter()
    completed = subprocess.run(
        command,
        capture_output=True,
        text=True,
        check=False,
        timeout=budget + timeout_padding,
        env=_clean_env(),
    )
    wall = round(time.perf_counter() - started, 1)
    parsed = parse_events(completed.stdout)
    parsed["wall_seconds"] = wall
    parsed["returncode"] = completed.returncode
    return parsed


def clone_public_repo(url: str, dest: Path) -> None:
    print(f"Cloning {url} (depth 1) into {dest}...", file=sys.stderr)
    subprocess.run(
        ["git", "clone", "--depth", "1", url, str(dest)],
        check=True,
    )


def compute_relative_savings(base_val: float, aug_val: float) -> str:
    if base_val == 0:
        return "N/A"
    delta = ((aug_val - base_val) / base_val) * 100.0
    return f"{delta:+.1f}%"


def generate_omp_markdown_report(report_data: dict) -> str:
    base = report_data["baseline"]
    aug = report_data["augmented"]

    in_save = compute_relative_savings(base["usage"]["input"], aug["usage"]["input"])
    out_save = compute_relative_savings(base["usage"]["output"], aug["usage"]["output"])
    call_save = compute_relative_savings(base["llm_calls"], aug["llm_calls"])
    cost_save = compute_relative_savings(base["usage"]["cost"], aug["usage"]["cost"])
    time_save = compute_relative_savings(base["wall_seconds"], aug["wall_seconds"])

    md = [
        "# null-space Empirical A/B Benchmark Report",
        f"\n**Timestamp:** {report_data['timestamp']}",
        f"**Model:** `{report_data['model']}`",
        f"**Target Codebase:** `{report_data['codebase']}`",
        f"**Task Symbol:** `{report_data['symbol']}` (Negative Check: {report_data['is_negative']})\n",
        "## Comparative Performance\n",
        "| Metric | Baseline (No MCP) | Augmented (+ null-space) | Delta / Savings |",
        "|---|---:|---:|---|",
        f"| **Input Tokens** | {base['usage']['input']:,} | {aug['usage']['input']:,} | **{in_save}** |",
        f"| **Output Tokens** | {base['usage']['output']:,} | {aug['usage']['output']:,} | **{out_save}** |",
        f"| **LLM Calls / Turns** | {base['llm_calls']} | {aug['llm_calls']} | **{call_save}** |",
        f"| **Wall-Clock Time** | {base['wall_seconds']}s | {aug['wall_seconds']}s | **{time_save}** |",
        f"| **Total Cost (USD)** | ${base['usage']['cost']:.4f} | ${aug['usage']['cost']:.4f} | **{cost_save}** |",
        "\n### Tool Executions Breakdown\n",
        f"- **Baseline Tool Calls:** `{json.dumps(base['tool_calls'])}`",
        f"- **Augmented Tool Calls:** `{json.dumps(aug['tool_calls'])}`",
    ]

    if report_data.get("otel_spans"):
        spans = report_data["otel_spans"]
        avg_dur = (
            sum(s.get("duration_ms", 0) for s in spans) / len(spans) if spans else 0
        )
        md.append(f"\n### OpenTelemetry Server Spans ({len(spans)} recorded)")
        md.append(f"- **Average Prune Latency:** {avg_dur:.3f} ms")

    md.append("\n---\n*Report generated by `null-space/evals/omp_eval.py`*")
    return "\n".join(md)


def main():
    parser = argparse.ArgumentParser(description="null-space omp A/B evaluation")
    parser.add_argument(
        "--model",
        default=DEFAULT_MODEL,
        help=f"Model identifier (default: {DEFAULT_MODEL})",
    )
    parser.add_argument(
        "--fixture",
        default="messy",
        help="Fixture name (messy)",
    )
    parser.add_argument(
        "--repo",
        help="Optional public GitHub repository URL to clone and benchmark (e.g. https://github.com/fastapi/fastapi)",
    )
    parser.add_argument(
        "--symbol",
        default="ExponentialBackoffRetryStrategy",
        help="Target symbol or class to search for in the task prompt",
    )
    parser.add_argument(
        "--negative-check",
        action="store_true",
        help="Whether this task asks to verify an absent/non-existent symbol",
    )
    parser.add_argument(
        "--max-time",
        default="5m",
        help="Maximum time budget per arm (default 5m)",
    )
    parser.add_argument(
        "--omp-bin",
        default="omp",
        help="Path to omp binary (default 'omp')",
    )
    args = parser.parse_args()

    ensure_binary()
    RESULTS_DIR.mkdir(parents=True, exist_ok=True)

    base_task, aug_task = generate_task_prompt(args.symbol, args.negative_check)

    with tempfile.TemporaryDirectory(prefix="null_space_eval_") as tmp:
        tmp_path = Path(tmp)
        if args.repo:
            source_dir = tmp_path / "cloned_repo"
            clone_public_repo(args.repo, source_dir)
            codebase_name = args.repo
        else:
            source_dir = REPO_ROOT / "evals" / "messy_codebase"
            codebase_name = "evals/messy_codebase"

        baseline_repo, augmented_repo, trace_path = prepare_fixture_copies(
            tmp_path, source_dir
        )

        print(f"\n=== Running Baseline Arm (No MCP) on {codebase_name} ===")
        print(f"Task: {base_task}\n")
        with isolated_user_mcp():
            base_res = run_arm(
                args.omp_bin,
                baseline_repo,
                args.model,
                args.max_time,
                task=base_task,
            )

        print(
            f"Baseline completed in {base_res['wall_seconds']}s (LLM calls: {base_res['llm_calls']}, input tokens: {base_res['usage']['input']})"
        )

        print(f"\n=== Running Augmented Arm (+ null-space) on {codebase_name} ===")
        print(f"Task: {aug_task}\n")
        with isolated_user_mcp():
            aug_res = run_arm(
                args.omp_bin,
                augmented_repo,
                args.model,
                args.max_time,
                task=aug_task,
            )

        print(
            f"Augmented completed in {aug_res['wall_seconds']}s (LLM calls: {aug_res['llm_calls']}, input tokens: {aug_res['usage']['input']})"
        )

        otel_spans = []
        if trace_path.exists():
            for line in trace_path.read_text().splitlines():
                if line.strip().startswith("{"):
                    try:
                        otel_spans.append(json.loads(line))
                    except Exception:
                        pass

        report = {
            "timestamp": datetime.now(UTC).isoformat(),
            "model": args.model,
            "codebase": codebase_name,
            "symbol": args.symbol,
            "is_negative": args.negative_check,
            "baseline": base_res,
            "augmented": aug_res,
            "otel_spans": otel_spans,
        }

        ts_str = datetime.now(UTC).strftime("%Y%m%dT%H%M%SZ")
        json_path = RESULTS_DIR / f"omp_report_{ts_str}.json"
        md_path = RESULTS_DIR / f"omp_report_{ts_str}.md"

        json_path.write_text(json.dumps(report, indent=2))
        md_content = generate_omp_markdown_report(report)
        md_path.write_text(md_content)

        print("\n" + md_content)
        print(f"\nSaved report to:\n  {json_path}\n  {md_path}")


if __name__ == "__main__":
    main()
