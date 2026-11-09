package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type dependency struct {
	Path    string
	Version string
	Dir     string
}

type licenseFile struct {
	Path   string
	SHA256 string
}

type moduleRecord struct {
	Path         string
	Version      string
	LicenseBasis string
	LicenseFiles []licenseFile
}

type manifest struct {
	SchemaVersion int
	Targets       []string
	Modules       []moduleRecord
}

func main() {
	output := flag.String("output", "", "new directory where the generated license bundle is written")
	flag.Parse()
	if *output == "" || flag.NArg() != 0 {
		fatal(errors.New("usage: license-bundle --output NEW_DIRECTORY"))
	}
	absOutput, err := filepath.Abs(*output)
	if err != nil {
		fatal(err)
	}
	if _, err := os.Stat(absOutput); !os.IsNotExist(err) {
		fatal(fmt.Errorf("output directory must not already exist: %s", absOutput))
	}

	targets := []string{"./cmd/server", "./cmd/replica-ingress"}
	args := []string{"list", "-deps", "-f", "{{if .Module}}{{.Module.Path}}|{{.Module.Version}}|{{.Module.Dir}}{{end}}"}
	args = append(args, targets...)
	raw, err := exec.Command("go", args...).Output()
	if err != nil {
		fatal(fmt.Errorf("go list dependency modules: %w", err))
	}

	dependencies := map[string]dependency{}
	for _, line := range strings.Split(string(raw), "\n") {
		parts := strings.Split(line, "|")
		if len(parts) != 3 || parts[1] == "" {
			continue
		}
		dep := dependency{Path: parts[0], Version: parts[1], Dir: parts[2]}
		dependencies[dep.Path+"@"+dep.Version] = dep
	}
	if len(dependencies) == 0 {
		fatal(errors.New("go list returned no versioned dependencies"))
	}

	ids := make([]string, 0, len(dependencies))
	for id := range dependencies {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	if err := os.MkdirAll(filepath.Join(absOutput, "modules"), 0o755); err != nil {
		fatal(err)
	}
	records := make([]moduleRecord, 0, len(ids))
	totalFiles := 0
	for _, id := range ids {
		dep := dependencies[id]
		if err := validateModulePath(dep.Path, dep.Version); err != nil {
			fatal(err)
		}
		entries, err := os.ReadDir(dep.Dir)
		if err != nil {
			fatal(fmt.Errorf("read module %s: %w", id, err))
		}
		var names []string
		for _, entry := range entries {
			if entry.IsDir() || !isLicenseMaterial(entry.Name()) {
				continue
			}
			names = append(names, entry.Name())
		}
		sort.Strings(names)
		goHiveEvidence := dep.Path == "sqlflow.org/gohive" && !hasLicenseTerms(names)
		if !hasLicenseTerms(names) && !goHiveEvidence {
			fatal(fmt.Errorf("dependency %s has no top-level LICENSE or COPYING terms file", id))
		}

		moduleDir := filepath.Join(absOutput, "modules", filepath.FromSlash(id))
		if err := os.MkdirAll(moduleDir, 0o755); err != nil {
			fatal(err)
		}
		record := moduleRecord{Path: dep.Path, Version: dep.Version}
		for _, name := range names {
			data, err := os.ReadFile(filepath.Join(dep.Dir, name))
			if err != nil {
				fatal(err)
			}
			target := filepath.Join(moduleDir, name)
			if err := os.WriteFile(target, data, 0o644); err != nil {
				fatal(err)
			}
			record.LicenseFiles = append(record.LicenseFiles, licenseFile{
				Path:   filepath.ToSlash(filepath.Join("modules", filepath.FromSlash(id), name)),
				SHA256: sha256Hex(data),
			})
			totalFiles++
		}
		if goHiveEvidence {
			statement, err := goHiveLicenseStatement(dep.Dir)
			if err != nil {
				fatal(fmt.Errorf("record gohive license evidence: %w", err))
			}
			name := "README-license-statement.txt"
			if err := os.WriteFile(filepath.Join(moduleDir, name), statement, 0o644); err != nil {
				fatal(err)
			}
			record.LicenseBasis = "Pinned module README states Apache License 2.0; the complete license text is included as /app/LICENSE."
			record.LicenseFiles = append(record.LicenseFiles, licenseFile{
				Path:   filepath.ToSlash(filepath.Join("modules", filepath.FromSlash(id), name)),
				SHA256: sha256Hex(statement),
			})
			totalFiles++
		} else {
			record.LicenseBasis = "Unmodified top-level license, copying, notice, or copyright files from the pinned Go module source."
		}
		records = append(records, record)
	}

	readme := []byte("# Go dependency license materials\n\n" +
		"This bundle contains unchanged top-level license, copying, notice, and copyright files for the versioned Go modules imported by ./cmd/server and ./cmd/replica-ingress. See MANIFEST.json for module versions, file paths, and SHA-256 hashes.\n\n" +
		"The pinned sqlflow.org/gohive module has no top-level license file. Its upstream README license statement is included as evidence; it declares Apache License 2.0, whose complete terms are also shipped at /app/LICENSE. The generator fails closed for any other dependency that lacks license materials.\n\n" +
		"This is an artifact notice bundle, not a legal compatibility opinion.\n")
	if err := os.WriteFile(filepath.Join(absOutput, "README.md"), readme, 0o644); err != nil {
		fatal(err)
	}
	data, err := json.MarshalIndent(manifest{
		SchemaVersion: 1,
		Targets:       targets,
		Modules:       records,
	}, "", "  ")
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Join(absOutput, "MANIFEST.json"), append(data, '\n'), 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("Go license bundle: %d modules, %d unmodified license/notice materials, output %s\n", len(records), totalFiles, absOutput)
}

func validateModulePath(modulePath, version string) error {
	clean := path.Clean(modulePath)
	if clean != modulePath || path.IsAbs(modulePath) || clean == "." || strings.HasPrefix(clean, "../") ||
		strings.Contains(version, "/") || version == "" {
		return fmt.Errorf("unsafe or incomplete module identity %q@%q", modulePath, version)
	}
	return nil
}

func isLicenseMaterial(name string) bool {
	lower := strings.ToLower(name)
	for _, prefix := range []string{"license", "copying", "notice", "copyright"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func hasLicenseTerms(names []string) bool {
	for _, name := range names {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "license") || strings.HasPrefix(lower, "copying") {
			return true
		}
	}
	return false
}

func goHiveLicenseStatement(moduleDir string) ([]byte, error) {
	readme, err := os.ReadFile(filepath.Join(moduleDir, "README.md"))
	if err != nil {
		return nil, err
	}
	content := string(readme)
	start := strings.Index(content, "## License")
	if start < 0 {
		return nil, errors.New("pinned README no longer has a License section")
	}
	section := content[start:]
	if next := strings.Index(section[len("## License"):], "\n## "); next >= 0 {
		section = section[:len("## License")+next+1]
	}
	if !strings.Contains(strings.ToLower(section), "apache license 2.0") {
		return nil, errors.New("pinned README License section no longer declares Apache License 2.0")
	}
	return []byte(strings.TrimSpace(section) + "\n"), nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
