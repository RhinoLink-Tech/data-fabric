#!/usr/bin/env python3
"""Build WorkBuddy submission archives from the maintained Data Fabric skill."""
import argparse
from pathlib import Path
from zipfile import ZIP_DEFLATED, ZipFile

ROOT = Path(__file__).resolve().parents[2]
HERE = Path(__file__).resolve().parent
SKILL = ROOT / "skills" / "data-fabric-query"
MARKET_FIELDS = """display_name: Data Fabric 数据连接与查询
display_name_en: Data Fabric Data Access
description_zh: 查看授权数据源和表结构并执行只读查询；较新服务可支持类型发现和已有数据源管理。
description_en: Inspect authorized sources and schemas and run read-only queries; newer servers can offer type discovery and existing-source management.
examples_zh:
  - 列出我有权限访问的数据源。
  - 查看订单表字段并统计本月订单数量。
  - 如果当前服务支持类型发现，列出支持的数据库和文件格式。
examples_en:
  - List the data sources I can access.
  - Inspect the orders table and count this month's orders.
  - If this server supports type discovery, list its databases and file formats.
version: 1.1.0
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
                content = ("---\n" + front + "\n" + MARKET_FIELDS + "---\n" + body).encode("utf-8")
            files[rel] = content
    if "SKILL.md" not in files:
        raise ValueError("Missing SKILL.md")
    return files


def write_zip(path, files):
    with ZipFile(path, "w", ZIP_DEFLATED) as archive:
        for name, content in sorted(files.items()):
            archive.writestr(name, content)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path, help="Output directory for the two ZIP files")
    output = parser.parse_args().output
    output.mkdir(parents=True, exist_ok=True)
    skill = skill_files()
    connector = {name: (HERE / name).read_bytes() for name in
                 ("connector-meta.json", "mcp.json", "token-schema.json")}
    connector["icon.png"] = (ROOT / "assets" / "data-fabric-icon.png").read_bytes()
    connector.update({"skills/data-fabric-query/" + name: value for name, value in skill.items()})
    write_zip(output / "data-fabric-workbuddy-connector.zip", connector)
    write_zip(output / "data-fabric-workbuddy-skill.zip",
              {"data-fabric-query/" + name: value for name, value in skill.items()})
    print("Created WorkBuddy connector and standalone skill packages in", output)


if __name__ == "__main__":
    main()
