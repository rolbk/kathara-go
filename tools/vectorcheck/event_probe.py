#!/usr/bin/env python3
"""Layer B authority runner for the ``event`` package.

``event/`` is a faithful port (PORT_SPEC §3.1 row 13) of
``Kathara/event/EventDispatcher.py`` with the ``getattr`` dispatch replaced by
typed payloads (§0.2 #13). Two things about it cannot be read off the source,
so they are measured here:

  1. **The catalog.** Which events exist, and what each dispatch site passes.
     The names are a contract — ``cli/ui/event/register.py`` subscribes by them
     — so the answer is taken by ast-walking every
     ``EventDispatcher.dispatch/register/unregister`` call in 3.8.3 rather than
     by reading a list someone maintained by hand.

  2. **What a subscriber sees when a subscriber mutates the table.** ``dispatch``
     iterates the live list at ``self.events[event]`` while ``unregister``
     deletes the *key*, so subscribing, unsubscribing or re-subscribing from
     inside a callback produces behaviour that belongs to CPython's list and
     dict semantics and to nothing the source says. Each scenario is executed
     against the real dispatcher and every callback that fires is written down,
     in order.

Results go to ``event/testdata/catalog.json`` and
``event/testdata/dispatch_traces.json``, consumed by the Go tests in
``event/events_test.go`` and ``event/oracle_test.go``.

The Python behaviour is the truth. When Go disagrees, Go is wrong.

Usage:

    /root/kathara/pyvenv/bin/python tools/vectorcheck/event_probe.py [--out-dir PATH]
"""

from __future__ import annotations

import argparse
import ast
import json
import os
import sys

KATHARA_SRC = "/root/kathara/kathara-python/src"
sys.path.insert(0, KATHARA_SRC)

from Kathara.event.EventDispatcher import EventDispatcher  # noqa: E402

PACKAGE_ROOT = os.path.join(KATHARA_SRC, "Kathara")

# The two events the traces drive. Real catalog names, so the KeyError text a
# trace records is the text the Go port has to produce.
E1 = "link_deployed"
E2 = "machine_deployed"


# ---------------------------------------------------------------------------
# 1. The catalog
# ---------------------------------------------------------------------------

def build_catalog() -> dict:
    """Walk every .py under Kathara/ and collect the dispatcher call sites."""
    dispatch: dict[str, dict] = {}
    registrations: list[dict] = []
    unregistrations: list[dict] = []

    for dirpath, _, filenames in os.walk(PACKAGE_ROOT):
        for filename in sorted(filenames):
            if not filename.endswith(".py"):
                continue
            path = os.path.join(dirpath, filename)
            with open(path, encoding="utf-8") as handle:
                tree = ast.parse(handle.read(), filename=path)
            site_prefix = os.path.relpath(path, KATHARA_SRC)

            for node in ast.walk(tree):
                if not isinstance(node, ast.Call) or not isinstance(node.func, ast.Attribute):
                    continue
                method = node.func.attr
                if method not in ("dispatch", "register", "unregister"):
                    continue
                # Only literal event names: every call site in 3.8.3 is one.
                if not node.args or not isinstance(node.args[0], ast.Constant):
                    continue
                if not isinstance(node.args[0].value, str):
                    continue

                name = node.args[0].value
                site = f"{site_prefix}:{node.lineno}"

                if method == "dispatch":
                    entry = dispatch.setdefault(name, {"sites": []})
                    entry["sites"].append({
                        "site": site,
                        "kwargs": [kw.arg for kw in node.keywords],
                    })
                elif method == "register":
                    # register(event, obj, method=None) — a falsy method means
                    # `run` (EventDispatcher.py:67).
                    subscriber = ast.unparse(node.args[1]) if len(node.args) >= 2 else None
                    callback = None
                    if len(node.args) >= 3 and isinstance(node.args[2], ast.Constant):
                        callback = node.args[2].value
                    registrations.append({
                        "event": name, "site": site,
                        "obj": subscriber, "method": callback,
                    })
                else:
                    unregistrations.append({"event": name, "site": site})

    return {
        "note": "Generated from Kathara 3.8.3 by ast-walking every "
                "EventDispatcher.dispatch/register/unregister call site.",
        "events": [
            {
                "name": name,
                "kwargs": sorted({k for s in entry["sites"] for k in s["kwargs"]}),
                "sites": entry["sites"],
            }
            for name, entry in sorted(dispatch.items())
        ],
        "cli_registrations": registrations,
        "cli_unregistrations": unregistrations,
    }


# ---------------------------------------------------------------------------
# 2. The traces
# ---------------------------------------------------------------------------
#
# Log grammar, one flat list per scenario:
#
#   run:<tag>              a run callback fired
#   hook:<tag>             an unregister callback fired
#   err:<Class>:<msg>      the step raised
#   subs:<tag>,<tag>       get_subscribers() returned these, in order
#   subs:!<Class>:<msg>    get_subscribers() raised

def reg(event, tag, hook=False, on_run=None, on_hook=None):
    step = {"op": "register", "event": event, "tag": tag, "hook": hook}
    if on_run:
        step["on_run"] = on_run
    if on_hook:
        step["on_hook"] = on_hook
    return step


def dis(event):
    return {"op": "dispatch", "event": event}


def unreg(event):
    return {"op": "unregister", "event": event}


def subs(event):
    return {"op": "subscribers", "event": event}


def a_reg(event, tag, hook=False):
    return {"do": "register", "event": event, "tag": tag, "hook": hook}


def a_unreg(event):
    return {"do": "unregister", "event": event}


def a_dis(event):
    return {"do": "dispatch", "event": event}


def a_raise(msg):
    return {"do": "raise", "msg": msg}


SCENARIOS = [
    {
        "name": "registration_order",
        "note": "EventDispatcher.py:62-70 appends to a list; dispatch (L85) walks it in order.",
        "steps": [reg(E1, "a"), reg(E1, "b"), reg(E1, "c"), dis(E1), subs(E1)],
    },
    {
        "name": "two_events_are_independent",
        "note": "self.events is keyed by name; interleaved registrations do not mix.",
        "steps": [reg(E1, "a1"), reg(E2, "b1"), reg(E1, "a2"), reg(E2, "b2"),
                  dis(E1), dis(E2), subs(E1), subs(E2)],
    },
    {
        "name": "dispatch_unknown_event_is_a_noop",
        "note": "EventDispatcher.py:82-83 `if event not in self.events: return`.",
        "steps": [dis(E1), reg(E2, "b"), dis(E1), dis(E2)],
    },
    {
        "name": "unregister_unknown_event_is_a_noop",
        "note": "EventDispatcher.py:97-98.",
        "steps": [unreg(E1), reg(E1, "a", hook=True), unreg(E1), unreg(E1), dis(E1)],
    },
    {
        "name": "subscribers_unknown_event_raises_keyerror",
        "note": "get_subscribers (L46) indexes self.events[event] with no guard.",
        "steps": [subs(E1), reg(E1, "a"), subs(E1), unreg(E1), subs(E1)],
    },
    {
        "name": "unregister_fires_hooks_in_registration_order",
        "note": "EventDispatcher.py:100-102; subscribers without an `unregister` "
                "attribute contribute None and are skipped.",
        "steps": [reg(E1, "a", hook=True), reg(E1, "b"), reg(E1, "c", hook=True),
                  unreg(E1), subs(E1), dis(E1)],
    },
    {
        "name": "same_subscriber_registered_three_times",
        "note": "register.py:59-61 registers one HandleProgressBar under three "
                "events; per event the hook is stored per entry, so N entries "
                "mean N hook calls.",
        "steps": [reg(E1, "bar", hook=True), reg(E1, "bar", hook=True),
                  reg(E1, "bar", hook=True), dis(E1), unreg(E1)],
    },
    {
        "name": "double_unregister_of_one_event",
        "note": "register.py:37 and :44 both unregister machine_deployed; the "
                "second call finds no key and returns.",
        "steps": [reg(E2, "bar", hook=True), reg(E2, "term", hook=False),
                  unreg(E2), unreg(E2), dis(E2)],
    },
    {
        "name": "register_during_dispatch_is_seen_by_that_dispatch",
        "note": "dispatch iterates the live list object, so an append lands "
                "inside the loop and runs last.",
        "steps": [reg(E1, "registrar", on_run=[a_reg(E1, "late")]), reg(E1, "tail"),
                  dis(E1), subs(E1), dis(E1)],
    },
    {
        "name": "unregister_during_dispatch_still_runs_the_rest",
        "note": "unregister deletes the dict key but the loop holds the list "
                "object; every hook fires immediately, then the remaining run "
                "callbacks still execute.",
        "steps": [reg(E1, "unreg", hook=True, on_run=[a_unreg(E1)]),
                  reg(E1, "tail", hook=True),
                  dis(E1), subs(E1), dis(E1)],
    },
    {
        "name": "unregister_then_register_during_dispatch",
        "note": "the re-register builds a NEW list under the key; the in-flight "
                "loop keeps walking the old one, so `new` does not run now.",
        "steps": [reg(E1, "swap", on_run=[a_unreg(E1), a_reg(E1, "new")]),
                  reg(E1, "tail"),
                  dis(E1), subs(E1), dis(E1)],
    },
    {
        "name": "handler_exception_aborts_the_rest",
        "note": "dispatch has no try/except (L85-86): the exception propagates "
                "to the dispatching thread and later subscribers never run. "
                "The subscriber list is left intact.",
        "steps": [reg(E1, "head"), reg(E1, "boom", on_run=[a_raise("boom!")]),
                  reg(E1, "tail"),
                  dis(E1), subs(E1), dis(E1)],
    },
    {
        "name": "hook_exception_aborts_unregister_and_skips_the_delete",
        "note": "unregister (L100-104) raises out of the hook loop before "
                "`del self.events[event]`, so the event stays subscribed.",
        "steps": [reg(E1, "boom", hook=True, on_hook=[a_raise("hook boom")]),
                  reg(E1, "ok", hook=True),
                  unreg(E1), subs(E1), dis(E1), unreg(E1), subs(E1)],
    },
    {
        "name": "mutating_another_event_during_dispatch",
        "note": "touching a different key does not disturb the in-flight loop.",
        "steps": [reg(E2, "other", hook=True),
                  reg(E1, "mutator", on_run=[a_reg(E2, "added"), a_unreg(E2)]),
                  reg(E1, "tail"),
                  dis(E1), subs(E1), dis(E2)],
    },
    {
        "name": "reentrant_dispatch_of_another_event",
        "note": "nested dispatch runs to completion inside the outer one.",
        "steps": [reg(E2, "inner"),
                  reg(E1, "outer", on_run=[a_dis(E2)]),
                  reg(E1, "tail"),
                  dis(E1)],
    },
    {
        "name": "unregister_then_register_outside_dispatch",
        "note": "after the key is deleted, register starts a fresh list.",
        "steps": [reg(E1, "old", hook=True), unreg(E1), reg(E1, "new"),
                  dis(E1), subs(E1)],
    },
]


class Runner:
    """Executes one scenario against the real EventDispatcher singleton."""

    def __init__(self, dispatcher):
        self.dispatcher = dispatcher
        self.log = []

    def act(self, actions):
        """What a subscriber does from inside its callback."""
        for action in actions or []:
            do = action["do"]
            if do == "register":
                self.register(action["event"], action["tag"],
                              action.get("hook", False), None, None)
            elif do == "unregister":
                self.dispatcher.unregister(action["event"])
            elif do == "dispatch":
                self.dispatcher.dispatch(action["event"])
            elif do == "raise":
                raise RuntimeError(action["msg"])
            else:
                raise AssertionError(f"unknown action {do}")

    def register(self, event, tag, hook, on_run, on_hook):
        runner = self

        def run(self, **kwargs):
            runner.log.append(f"run:{tag}")
            runner.act(on_run)

        def unregister(self):
            runner.log.append(f"hook:{tag}")
            runner.act(on_hook)

        # A subscriber class per tag: `hasattr(obj, 'unregister')` must be
        # False for the no-hook case, so the attribute is simply absent.
        attrs = {"run": run, "tag": tag}
        if hook:
            attrs["unregister"] = unregister
        self.dispatcher.register(event, type(f"Sub_{tag}", (object,), attrs)())

    def step(self, step):
        op = step["op"]
        try:
            if op == "register":
                self.register(step["event"], step["tag"], step.get("hook", False),
                              step.get("on_run"), step.get("on_hook"))
            elif op == "dispatch":
                self.dispatcher.dispatch(step["event"])
            elif op == "unregister":
                self.dispatcher.unregister(step["event"])
            elif op == "subscribers":
                got = self.dispatcher.get_subscribers(step["event"])
                self.log.append("subs:" + ",".join(f.__self__.tag for f in got))
            else:
                raise AssertionError(f"unknown op {op}")
        except Exception as e:  # noqa: BLE001 - the trace records what escapes
            prefix = "subs:!" if op == "subscribers" else "err:"
            self.log.append(f"{prefix}{type(e).__name__}:{e}")


def build_traces() -> dict:
    dispatcher = EventDispatcher.get_instance()
    scenarios = []
    for scenario in SCENARIOS:
        # The dispatcher is a singleton (EventDispatcher.py:24-35), so
        # scenarios share it and start from an empty table.
        dispatcher.events.clear()
        runner = Runner(dispatcher)
        for step in scenario["steps"]:
            runner.step(step)
        scenarios.append({
            "name": scenario["name"],
            "note": scenario["note"],
            "steps": scenario["steps"],
            "log": runner.log,
        })

    return {
        "note": "Recorded from Kathara 3.8.3 EventDispatcher (CPython). Each "
                "scenario's steps are replayed against the Go dispatcher and "
                "the flat log must match entry for entry.",
        "scenarios": scenarios,
    }


def main() -> None:
    default_out = os.path.join(
        os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))),
        "event", "testdata",
    )
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out-dir", default=default_out,
                        help="directory to write catalog.json and dispatch_traces.json into")
    args = parser.parse_args()

    os.makedirs(args.out_dir, exist_ok=True)

    catalog = build_catalog()
    catalog_path = os.path.join(args.out_dir, "catalog.json")
    with open(catalog_path, "w", encoding="utf-8") as handle:
        json.dump(catalog, handle, indent=2)
        handle.write("\n")

    traces = build_traces()
    traces_path = os.path.join(args.out_dir, "dispatch_traces.json")
    with open(traces_path, "w", encoding="utf-8") as handle:
        json.dump(traces, handle, indent=2)
        handle.write("\n")

    sites = sum(len(e["sites"]) for e in catalog["events"])
    print(f"{len(catalog['events'])} events / {sites} dispatch sites "
          f"-> {catalog_path}")
    print(f"{len(traces['scenarios'])} scenarios -> {traces_path}")
    for scenario in traces["scenarios"]:
        print(f"  {scenario['name']}\n      {scenario['log']}")


if __name__ == "__main__":
    main()
