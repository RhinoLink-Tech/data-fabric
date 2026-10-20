# Third-party notices

Data Fabric's own source code is licensed under Apache-2.0; see the repository-level LICENSE file. The following Spec Kit scaffolding is separately licensed under the MIT License.

## GitHub Spec Kit

Source project: https://github.com/github/spec-kit
Checked upstream snapshot: https://github.com/github/spec-kit/tree/9111699cd27879e3e6301651a03e502ecb6dd65d
Upstream LICENSE: https://github.com/github/spec-kit/blob/9111699cd27879e3e6301651a03e502ecb6dd65d/LICENSE
Data Fabric import commit: 51e92036d2cf47cfb73e1ff7926368e01f33a203

The following files were imported or adapted from the upstream snapshot:

- .specify/scripts/powershell/*.ps1 - five files, unchanged from the upstream files at import.
- .specify/templates/{agent-file,checklist,plan,spec,tasks}-template.md - five files, unchanged from the upstream files at import.
- .claude/commands/speckit.*.md - nine files adapted from the corresponding upstream templates/commands/*.md files.

The PowerShell scripts and templates were compared byte-for-byte after line-ending normalization against the upstream snapshot. The Claude command files were adapted for this repository's command names and workflow. These files remain unchanged from their import commit.

### MIT License

Copyright GitHub, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
