package table_formats

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func (p *HudiMetadataParser) ParseHudiProperties(data []byte) (*HudiTableMetadata, error) {
	properties, err := parseHudiProperties(data)
	if err != nil {
		return nil, err
	}
	tableName := properties["hoodie.table.name"]
	if tableName == "" {
		return nil, fmt.Errorf("Hudi properties do not contain hoodie.table.name")
	}
	tableType := strings.ToUpper(properties["hoodie.table.type"])
	if tableType != string(CopyOnWriteTableType) && tableType != string(MergeOnReadTableType) {
		return nil, fmt.Errorf("unsupported or missing Hudi table type %q", tableType)
	}

	recordFields, err := parseHudiCreateSchema(properties["hoodie.table.create.schema"])
	if err != nil {
		return nil, err
	}
	recordKeys := splitHudiProperty(properties["hoodie.table.recordkey.fields"])
	if len(recordKeys) == 0 {
		recordKeys = splitHudiProperty(properties["hoodie.datasource.write.recordkey.field"])
	}
	partitionFields := splitHudiProperty(properties["hoodie.table.partition.fields"])
	if len(partitionFields) == 0 {
		partitionFields = splitHudiProperty(properties["hoodie.datasource.write.partitionpath.field"])
	}

	safeProperties := make(map[string]string)
	for _, key := range []string{
		"hoodie.table.name",
		"hoodie.table.type",
		"hoodie.table.version",
		"hoodie.table.timeline.path",
		"hoodie.table.recordkey.fields",
		"hoodie.table.partition.fields",
	} {
		if value := properties[key]; value != "" {
			safeProperties[key] = value
		}
	}

	return &HudiTableMetadata{
		TableName:       tableName,
		TableType:       tableType,
		RecordFields:    recordFields,
		PartitionFields: partitionFields,
		KeyField:        strings.Join(recordKeys, ","),
		Properties:      safeProperties,
		HudiVersion:     properties["hoodie.table.version"],
		CDCEnabled:      strings.EqualFold(properties["hoodie.table.cdc.enabled"], "true"),
	}, nil
}

func parseHudiProperties(data []byte) (map[string]string, error) {
	properties := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 4*1024*1024)
	logical := ""
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if logical != "" {
			line = strings.TrimLeft(line, " \t\f")
		} else {
			trimmed := strings.TrimLeft(line, " \t\f")
			if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "!") {
				continue
			}
		}
		logical += line
		if hasOddTrailingBackslashes(logical) {
			logical = logical[:len(logical)-1]
			continue
		}
		key, value, ok := splitHudiPropertyLine(logical)
		if !ok {
			return nil, fmt.Errorf("invalid Hudi property on line %d", lineNumber)
		}
		properties[unescapeHudiProperty(key)] = unescapeHudiProperty(value)
		logical = ""
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read Hudi properties: %w", err)
	}
	if logical != "" {
		key, value, ok := splitHudiPropertyLine(logical)
		if !ok {
			return nil, fmt.Errorf("invalid trailing Hudi property")
		}
		properties[unescapeHudiProperty(key)] = unescapeHudiProperty(value)
	}
	return properties, nil
}

func splitHudiPropertyLine(line string) (string, string, bool) {
	line = strings.TrimLeft(line, " \t\f")
	escaped := false
	separator := -1
	for index, char := range line {
		if escaped {
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			continue
		}
		if char == '=' || char == ':' || char == ' ' || char == '\t' || char == '\f' {
			separator = index
			break
		}
	}
	if separator < 0 {
		if line == "" {
			return "", "", false
		}
		return line, "", true
	}
	key := line[:separator]
	valueStart := separator
	for valueStart < len(line) && (line[valueStart] == ' ' || line[valueStart] == '\t' || line[valueStart] == '\f') {
		valueStart++
	}
	if valueStart < len(line) && (line[valueStart] == '=' || line[valueStart] == ':') {
		valueStart++
	}
	for valueStart < len(line) && (line[valueStart] == ' ' || line[valueStart] == '\t' || line[valueStart] == '\f') {
		valueStart++
	}
	return key, line[valueStart:], true
}

func hasOddTrailingBackslashes(value string) bool {
	count := 0
	for index := len(value) - 1; index >= 0 && value[index] == '\\'; index-- {
		count++
	}
	return count%2 == 1
}

func unescapeHudiProperty(value string) string {
	var out strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] != '\\' || index+1 >= len(value) {
			out.WriteByte(value[index])
			continue
		}
		index++
		switch value[index] {
		case 't':
			out.WriteByte('\t')
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 'f':
			out.WriteByte('\f')
		case 'u':
			if index+4 < len(value) {
				parsed, err := strconv.ParseUint(value[index+1:index+5], 16, 16)
				if err == nil {
					out.WriteRune(rune(parsed))
					index += 4
				} else {
					out.WriteByte('u')
				}
			} else {
				out.WriteByte('u')
			}
		default:
			out.WriteByte(value[index])
		}
	}
	return out.String()
}

func splitHudiProperty(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func parseHudiCreateSchema(schemaText string) ([]HudiField, error) {
	if strings.TrimSpace(schemaText) == "" {
		return nil, nil
	}
	var schema map[string]json.RawMessage
	if err := json.Unmarshal([]byte(schemaText), &schema); err != nil {
		return nil, fmt.Errorf("parse hoodie.table.create.schema: %w", err)
	}
	var rawFields []map[string]json.RawMessage
	if err := json.Unmarshal(schema["fields"], &rawFields); err != nil {
		return nil, fmt.Errorf("parse Hudi record fields: %w", err)
	}
	fields := make([]HudiField, 0, len(rawFields))
	for index, field := range rawFields {
		var name string
		if err := json.Unmarshal(field["name"], &name); err != nil || name == "" {
			return nil, fmt.Errorf("Hudi schema field %d has no name", index)
		}
		typeName, nullable, err := parseHudiAvroType(field["type"])
		if err != nil {
			return nil, fmt.Errorf("parse Hudi schema field %q: %w", name, err)
		}
		fields = append(fields, HudiField{
			Name: name, Type: ConvertHudiTypeToStandardType(typeName), Nullable: nullable,
		})
	}
	return fields, nil
}

func parseHudiAvroType(raw json.RawMessage) (string, bool, error) {
	if len(raw) == 0 {
		return "", false, fmt.Errorf("type is missing")
	}
	var primitive string
	if err := json.Unmarshal(raw, &primitive); err == nil {
		return primitive, false, nil
	}
	var union []json.RawMessage
	if err := json.Unmarshal(raw, &union); err == nil {
		nullable := false
		for _, member := range union {
			name, isNull, err := parseHudiAvroType(member)
			if err != nil {
				return "", false, err
			}
			if strings.EqualFold(name, "null") || isNull {
				nullable = true
				continue
			}
			return name, nullable, nil
		}
		return "null", nullable, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err == nil {
		if nested, ok := object["type"]; ok {
			name, nullable, err := parseHudiAvroType(nested)
			if err == nil {
				if name == "record" || name == "array" || name == "map" {
					return strings.ToUpper(name), nullable, nil
				}
				return name, nullable, nil
			}
		}
	}
	return "", false, fmt.Errorf("unsupported Avro type %s", string(raw))
}
