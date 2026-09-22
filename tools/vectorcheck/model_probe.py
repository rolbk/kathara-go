#!/usr/bin/env python3
"""Layer B authority runner for the ``model`` package."""

from __future__ import annotations

import argparse
import json
import os
import sys
from typing import Any

sys.path.insert(0, "/root/kathara/kathara-python/src")

from Kathara.model.Lab import Lab  # noqa: E402
from Kathara.model.Machine import Machine  # noqa: E402

# The six containers Machine.__init__ seeds; everything else in meta is a scalar
# the Go implementation keeps in a typed field or in Meta.Extras.
CONTAINERS = ["exec_commands", "sysctls", "envs", "ports", "ulimits", "volumes"]


def type_name(value: Any) -> str:
    return type(value).__name__


def dump_meta(meta: dict) -> dict:
    """Serialise Machine.meta into a shape both implementations can produce."""
    scalars = {}
    for key, value in meta.items():
        if key in CONTAINERS:
            continue
        scalars[key] = {"type": type_name(value), "str": str(value)}

    return {
        "exec_commands": list(meta["exec_commands"]),
        "sysctls": [
            {"key": k, "type": type_name(v), "str": str(v)} for k, v in meta["sysctls"].items()
        ],
        "envs": [{"key": k, "value": v} for k, v in meta["envs"].items()],
        "ports": [
            {"host": k[0], "protocol": k[1], "guest": v} for k, v in meta["ports"].items()
        ],
        "ulimits": [
            {"key": k, "soft": str(v["soft"]), "hard": str(v["hard"])}
            for k, v in meta["ulimits"].items()
        ],
        "volumes": [
            {"key": k, "guest_path": v["guest_path"], "mode": v["mode"]}
            for k, v in meta["volumes"].items()
        ],
        "scalars": scalars,
    }


def run(fn) -> dict:
    """Call fn, recording either its result or the exception class and message."""
    try:
        return {"ok": True, "result": fn()}
    except Exception as e:  # noqa: BLE001 - the class is the measurement
        return {"ok": False, "error_class": type(e).__name__, "error_message": str(e)}


def stringify(outcome: dict) -> dict:
    """Render a successful result as (type, str) so Go can compare it exactly.

    An accessor can answer None, a str, an int (of any width) or a bool, and the
    JSON number type cannot carry the widest of those; the pair keeps both the
    Python type and its exact rendering.
    """
    if not outcome["ok"]:
        return outcome
    result = outcome.pop("result")
    outcome["result_type"] = type_name(result)
    outcome["result_str"] = "" if result is None else str(result)
    return outcome


def probe_add_meta() -> list:
    """Every add_meta branch, with the stored state or the exception."""
    cases = []
    for meta_name, value in [
        # generic / scalar
        ("image", "kathara/frr"),
        ("mem", "512m"),
        ("cpus", "1.5"),
        ("num_terms", "2"),
        ("ipv6", "true"),
        ("shell", "/bin/bash"),
        ("entrypoint", "/bin/sh -c"),
        ("args", "--foo bar"),
        ("bridged_iface", "1"),
        ("unknown_option", "x"),
        # bool
        ("privileged", "1"),
        ("privileged", "yes"),
        ("privileged", "FALSE"),
        ("privileged", "maybe"),
        ("bridged", "true"),
        ("bridged", "off"),
        ("bridged", "nope"),
        # exec
        ("exec", "echo one"),
        # sysctl
        ("sysctl", "net.ipv4.ip_forward=1"),
        ("sysctl", "net.ipv4.conf.all.rp_filter=-1"),
        ("sysctl", "net.custom.value=abc"),
        ("sysctl", "net.a.b=  5  "),
        ("sysctl", "net.a.b=007"),
        ("sysctl", "net.a.b=٣"),
        ("sysctl", "net.a.b=+5"),
        ("sysctl", "net.a.b=1_0"),
        ("sysctl", "net.a.b="),
        ("sysctl", "net.a.b=1\n"),
        ("sysctl", "net.a.b=1\nx"),
        ("sysctl", "net.ключ.b=1"),
        ("sysctl", "net.a-b.c=1"),
        ("sysctl", "net.a.b=4096 87380 33554432"),
        ("sysctl", "net.a.b=-1-"),
        ("sysctl", "net.a.b=--5"),
        ("sysctl", "net.a.b=²"),
        ("sysctl", "net.a.b=½"),
        ("sysctl", "net.foo=1"),
        ("sysctl", "kernel.shm_rmid_forced=1"),
        ("sysctl", "kernel.shm_rmid_forced"),
        ("sysctl", "net.a.b=1=2"),
        ("sysctl", "net.a.b =5"),
        # env
        ("env", "MY_ENV_VAR=test"),
        ("env", "MY_ENV_VAR=1"),
        ("env", "K=  v  "),
        ("env", "K="),
        ("env", "K=1=2"),
        ("env", "K=v\n"),
        ("env", "K=v\nx"),
        ("env", "ключ=1"),
        ("env", "IFACES=linux:eth0,name=iface/name"),
        ("env", "MY_ENV_VAR"),
        ("env", "=1"),
        ("env", "a b=1"),
        # ulimit
        ("ulimit", "nofile=1024:2048"),
        ("ulimit", "nofile=1024"),
        ("ulimit", "memlock=-1"),
        ("ulimit", "nofile=2048:1024"),
        ("ulimit", "nofile=2048:-1"),
        ("ulimit", "nofile=-1:-1"),
        ("ulimit", "nofile=007"),
        ("ulimit", "n=٣:٥"),
        ("ulimit", "ключ=5"),
        ("ulimit", "nofile=5\n"),
        ("ulimit", "nofile=-1:1024"),
        ("ulimit", "nofile=-1:007"),
        ("ulimit", "nofile=-1:99999999999999999999999999"),
        ("ulimit", "nofile=1024:-2"),
        ("ulimit", "nofile=1024:2048:4096"),
        ("ulimit", "nofile"),
        ("ulimit", "no file=1"),
        ("ulimit", "nofile=5\nx"),
        # port
        ("port", "8080"),
        ("port", "8080/udp"),
        ("port", "2000:8080/udp"),
        ("port", "80/TCP"),
        ("port", "0"),
        ("port", "+1"),
        ("port", "٣"),
        ("port", "1_0:2"),
        ("port", "8080\n"),
        ("port", "8080/ppp"),
        ("port", "8080/"),
        ("port", ":2000"),
        ("port", "/tcp"),
        ("port", "abc"),
        ("port", "1:2:3"),
        ("port", "80/tcp/x"),
        # volume (absolute host paths only: the key is os.path.abspath, and the
        # Go test cannot share this process's working directory)
        ("volume", "/host/a|/guest/a|rw"),
        ("volume", "/host/b|/guest/b"),
        ("volume", "/host/c|/guest/c|rx"),
        ("volume", "/host/d|/guest/d|ro"),
        ("volume", "/a||/b"),
        ("volume", "|/a|/b"),
        ("volume", "/h|/g\n"),
        ("volume", "/h|/g|xx"),
        ("volume", "/h"),
        ("volume", "/h|"),
        ("volume", "|"),
        ("volume", "/h|/g|rw|extra"),
    ]:
        lab = Lab("test_lab")
        machine = lab.new_machine("pc1")
        outcome = run(lambda: machine.add_meta(meta_name, value))
        case = {"meta": meta_name, "value": value}
        case.update(outcome)
        if outcome["ok"]:
            case["previous_is_none"] = outcome["result"] is None
            case["previous_type"] = type_name(outcome["result"])
            case["previous_str"] = str(outcome["result"])
            del case["result"]
            case["meta_after"] = dump_meta(machine.meta)
        cases.append(case)

    return cases


def probe_add_meta_twice() -> list:
    """Return the previous value, which LabParser uses when warning."""
    cases = []
    for meta_name, first, second in [
        ("image", "a", "b"),
        ("sysctl", "net.a.b=1", "net.a.b=2"),
        ("env", "K=1", "K=2"),
        ("port", "8080", "9090"),
        ("ulimit", "nofile=5", "nofile=6"),
        ("volume", "/h|/g", "/h|/g2|rw"),
        ("privileged", "yes", "no"),
        ("bridged", "1", "0"),
        ("exec", "a", "b"),
    ]:
        lab = Lab("test_lab")
        machine = lab.new_machine("pc1")
        machine.add_meta(meta_name, first)
        previous = machine.add_meta(meta_name, second)
        cases.append(
            {
                "meta": meta_name,
                "first": first,
                "second": second,
                "previous_is_none": previous is None,
                "previous_type": type_name(previous),
                "previous_str": str(previous),
            }
        )
    return cases


def probe_accessors() -> dict:
    """The lazily validated accessors, per stored value."""

    def with_meta(key: str, value: Any) -> Machine:
        lab = Lab("test_lab")
        machine = lab.new_machine("pc1")
        machine.meta[key] = value
        return machine

    mem = []
    for value in [
        "064m", "100", " 12 ", "0", "", "12K", "12G", "12B", "+5m", "5 m", "-5m",
        "١٢m", "9" * 30 + "m", "9" * 30, "1e3", "12x", "12.5", "0x10",
    ]:
        machine = with_meta("mem", value)
        mem.append({"value": value, **stringify(run(machine.get_mem))})

    cpu = []
    for value in [
        "0.5", "2", "1e3", "  3 ", "-1.5", "١٢", "1_0", "abc", "", "0x1", "nan", "infinity",
        # Exercise a very large exponent that still reaches the CPU parser.
        "1e300",
    ]:
        machine = with_meta("cpus", value)
        cpu.append(
            {
                "value": value,
                "one": stringify(run(machine.get_cpu)),
                "nano": stringify(run(lambda m=machine: m.get_cpu(multiplier=1000000000))),
            }
        )

    num_terms = []
    for value in ["2", "0", " 3 ", "٣", "1_0", "+4", "-1", "2.5", "", "9" * 30]:
        machine = with_meta("num_terms", value)
        num_terms.append({"value": value, **stringify(run(machine.get_num_terms))})

    ipv6 = []
    for value, kind in [
        (True, "bool"), (False, "bool"), ("true", "str"), ("False", "str"),
        ("yes", "str"), ("maybe", "str"), (1, "int"), (1.5, "float"),
    ]:
        machine = with_meta("ipv6", value)
        ipv6.append({"value": str(value), "kind": kind, **stringify(run(machine.is_ipv6_enabled))})

    return {"mem": mem, "cpu": cpu, "num_terms": num_terms, "ipv6": ipv6}


def probe_check() -> list:
    """Machine.check(), for every type meta['bridged_iface'] can hold.

    check() appends the meta to the interface-number list and lets ``list.sort()``
    decide, so the type matters: an int fills a slot, a float that is not
    integral fills nothing (``1.5`` does NOT complete ``{0}`` — truncating it
    would be wrong), a NaN sorts nowhere, an infinity sorts to one end and a
    str/list raises TypeError naming its own type. The ``list`` cases always use
    ``["x"]``, which ``model/oracle_test.go`` relies on.
    """
    cases = []
    for interfaces, bridged_iface, kind in [
        ([0, 1], None, ""),
        ([1, 0], None, ""),
        ([0, 2], 1, "int"),
        ([0, 2], "1", "str"),
        ([0], "1", "str"),
        ([], "1", "str"),
        ([], 0, "int"),
        ([2, 4], None, ""),
        ([1], None, ""),
        ([0], -1, "int"),
        ([0], 1.5, "float"),
        ([0], 1.0, "float"),
        ([], 0.5, "float"),
        ([], 0.0, "float"),
        ([0, 1], 2.5, "float"),
        ([0, 2], 1.0, "float"),
        ([0], float("nan"), "float"),
        ([], float("nan"), "float"),
        ([0, 1], float("nan"), "float"),
        ([0], float("inf"), "float"),
        ([0], float("-inf"), "float"),
        ([], float("inf"), "float"),
        ([0], True, "bool"),
        ([], False, "bool"),
        ([0, 2], True, "bool"),
        ([0], ["x"], "list"),
        ([], ["x"], "list"),
        ([0], 1e300, "float"),
    ]:
        lab = Lab("test_lab")
        machine = lab.new_machine("pc1")
        for number in interfaces:
            lab.connect_machine_obj_to_link(machine, f"cd{number}", machine_iface_number=number)
        if bridged_iface is not None:
            machine.meta["bridged_iface"] = bridged_iface

        outcome = run(machine.check)
        cases.append(
            {
                "interfaces": interfaces,
                "bridged_iface": "" if bridged_iface is None else str(bridged_iface),
                "bridged_iface_kind": kind,
                "ok": outcome["ok"],
                "error_class": outcome.get("error_class", ""),
                "error_message": outcome.get("error_message", ""),
                "order": list(machine.interfaces.keys()) if outcome["ok"] else [],
            }
        )
    return cases


def probe_lab() -> dict:
    """Identity, dependency order and the two rendered forms."""
    hashes = [
        {"source": name, "hash": Lab(name).hash}
        for name in ["", "x", "y", "test_lab", "kathara_vlab", "lab con spazi", "labò", "日本語"]
    ]

    dependencies = []
    for deps in [["pc3", "pc1", "pc2"], ["pc2", "pc2", "pc1"], [], ["zz"], ["pc4", "pc3", "pc2", "pc1"]]:
        lab = Lab("test_lab")
        for name in ["pc1", "pc2", "pc3", "pc4"]:
            lab.new_machine(name)
        lab.apply_dependencies(deps)
        dependencies.append({"dependencies": deps, "order": list(lab.machines.keys())})

    lab = Lab("mylab")
    lab.description = "d"
    lab.version = "1"
    lab.author = "a"
    lab.email = "e"
    lab.web = "w"
    machine, _ = lab.connect_machine_to_link("pc1", "A", mac_address="00:00:00:00:00:01")
    lab.connect_machine_to_link("pc1", "B")
    machine.add_meta("bridged", "false")
    machine.add_meta("sysctl", "net.a.b=1")
    machine.add_meta("port", "8080")

    return {
        "hashes": hashes,
        "dependencies": dependencies,
        "lab_str": str(lab),
        "machine_str": str(machine),
    }


def probe_names() -> list:
    """Machine.__init__'s strip-then-match device-name rule."""
    cases = []
    for name in [
        "pc1", " pc1 ", "pc1\n", "\x1cpc1", "\xa0pc1\xa0", "_x", "a" * 30, "a" * 31,
        "PC1", "pc-1", "pc.1", "", "shared", "_test", "pc1​",
    ]:
        lab = Lab("test_lab")
        outcome = stringify(run(lambda n=name: Machine(lab, n).name))
        cases.append({"name": name, **outcome})
    return cases


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--out",
        default=os.path.join(os.path.dirname(__file__), "..", "..", "model", "testdata", "model_oracle.json"),
    )
    args = parser.parse_args()

    document = {
        "add_meta": probe_add_meta(),
        "add_meta_twice": probe_add_meta_twice(),
        "accessors": probe_accessors(),
        "check": probe_check(),
        "lab": probe_lab(),
        "names": probe_names(),
    }

    out = os.path.abspath(args.out)
    os.makedirs(os.path.dirname(out), exist_ok=True)
    with open(out, "w", encoding="utf-8") as fd:
        json.dump(document, fd, indent=2, ensure_ascii=False, sort_keys=True)
        fd.write("\n")

    print(f"wrote {out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
