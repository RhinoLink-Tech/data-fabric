#!/usr/bin/env python3
"""Build WorkBuddy submission archives from the maintained Data Fabric skill."""
import argparse
import json
from pathlib import Path
from zipfile import ZIP_DEFLATED, ZipFile, ZipInfo

ROOT = Path(__file__).resolve().parents[2]
HERE = Path(__file__).resolve().parent
SKILL = ROOT / "skills" / "data-fabric-query"
MARKET_FIELDS = """display_name: Data Fabric 数据源管理与查询
display_name_en: Data Fabric Source Management and Query
description_zh: 连接 Data Fabric 查询和管理数据源；服务启用安全向导时可引导新增。
description_en: Query and manage sources; guide source creation when the server enables secure onboarding.
examples_zh:
  - 当前服务支持哪些数据库和文件格式？
  - 服务启用安全向导时，帮我添加 PostgreSQL 数据源。
  - 查看订单表结构并统计本月订单数量。
examples_en:
  - Which databases and file formats does this server support?
  - Guide me to add PostgreSQL when secure onboarding is enabled.
  - Inspect the orders table and count orders this month.
version: 1.2.0
author: RhinoLink Tech
"""


def skill_files():
    files = {}
    for path in sorted(SKILL.rglob("*")):
        if path.is_file():
            rel = path.relative_to(SKILL).as_posix()
            content = path.read_bytes()
            if rel == "SKILL.md":
                text = content.decode("utf-8")
                if not text.startswith("---\n") or "\n---\n" not in text[4:]:
                    raise ValueError("Expected skill YAML frontmatter")
                front, body = text[4:].split("\n---\n", 1)
                front = front.replace("name: data-fabric-query", "name: data-fabric-access", 1)
                content = ("---\n" + front + "\n" + MARKET_FIELDS + "---\n" + body).encode("utf-8")
            files[rel] = content
    if "SKILL.md" not in files:
        raise ValueError("Missing SKILL.md")
    return files


def write_zip(path, files):
    with ZipFile(path, "w", ZIP_DEFLATED) as archive:
        for name, content in sorted(files.items()):
            entry = ZipInfo(name, (1980, 1, 1, 0, 0, 0))
            entry.compress_type = ZIP_DEFLATED
            entry.external_attr = 0o100644 << 16
            archive.writestr(entry, content)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path, help="Output directory for the two ZIP files")
    parser.add_argument("--connector-source", choices=("data-fabric", "data-fabric-access"), default="data-fabric", help="Connector source ID; must be available in the target WorkBuddy account")
    args = parser.parse_args()
    output = args.output
    output.mkdir(parents=True, exist_ok=True)
    skill = skill_files()
    connector = {name: (HERE / name).read_bytes() for name in
                 ("connector-meta.json", "mcp.json", "token-schema.json")}
    if args.connector_source != "data-fabric":
        meta = json.loads(connector["connector-meta.json"])
        config = json.loads(connector["mcp.json"])
        meta["source"] = args.connector_source
        config["mcpServers"][args.connector_source] = config["mcpServers"].pop("data-fabric")
        connector["connector-meta.json"] = json.dumps(meta, ensure_ascii=False, indent=2).encode("utf-8")
        connector["mcp.json"] = json.dumps(config, ensure_ascii=False, indent=2).encode("utf-8")
    connector["icon.png"] = (ROOT / "assets" / "data-fabric-icon.png").read_bytes()
    connector.update({"skills/data-fabric-access/" + name: value for name, value in skill.items()})
    write_zip(output / "data-fabric-workbuddy-connector.zip", connector)
    write_zip(output / "data-fabric-workbuddy-skill.zip",
              {"data-fabric-access/" + name: value for name, value in skill.items()})
    print("Created WorkBuddy connector and standalone skill packages in", output)


if __name__ == "__main__":
    main()
