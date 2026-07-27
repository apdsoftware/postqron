package adminconsole

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
)

type tabularExport struct {
	filename string
	headers  []string
	rows     [][]string
}

func writeTabularExport(
	writer http.ResponseWriter,
	format string,
	data tabularExport,
) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "csv":
		content, err := encodeCSV(data)
		if err != nil {
			writeError(writer, http.StatusServiceUnavailable, "ADMIN_UNAVAILABLE")
			return
		}
		writeDownload(
			writer,
			data.filename+".csv",
			"text/csv; charset=utf-8",
			content,
		)
	case "xlsx":
		content, err := encodeXLSX(data)
		if err != nil {
			writeError(writer, http.StatusServiceUnavailable, "ADMIN_UNAVAILABLE")
			return
		}
		writeDownload(
			writer,
			data.filename+".xlsx",
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
			content,
		)
	default:
		writeError(writer, http.StatusBadRequest, "ADMIN_INVALID_EXPORT_FORMAT")
	}
}

func writeDownload(
	writer http.ResponseWriter,
	filename, contentType string,
	content []byte,
) {
	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("Content-Disposition", fmt.Sprintf(
		`attachment; filename="%s"`,
		filename,
	))
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(content)
}

func encodeCSV(data tabularExport) ([]byte, error) {
	var content bytes.Buffer
	content.WriteString("\xEF\xBB\xBF")
	output := csv.NewWriter(&content)
	if err := output.Write(safeSpreadsheetRow(data.headers)); err != nil {
		return nil, err
	}
	for _, row := range data.rows {
		if err := output.Write(safeSpreadsheetRow(row)); err != nil {
			return nil, err
		}
	}
	output.Flush()
	if err := output.Error(); err != nil {
		return nil, err
	}
	return content.Bytes(), nil
}

func encodeXLSX(data tabularExport) ([]byte, error) {
	var content bytes.Buffer
	archive := zip.NewWriter(&content)
	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
  <Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
</Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
</Relationships>`,
		"xl/workbook.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <sheets><sheet name="Export" sheetId="1" r:id="rId1"/></sheets>
</workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
</Relationships>`,
		"xl/worksheets/sheet1.xml": worksheetXML(data),
	}
	for _, name := range []string{
		"[Content_Types].xml",
		"_rels/.rels",
		"xl/workbook.xml",
		"xl/_rels/workbook.xml.rels",
		"xl/worksheets/sheet1.xml",
	} {
		entry, err := archive.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write([]byte(files[name])); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return content.Bytes(), nil
}

func worksheetXML(data tabularExport) string {
	var content strings.Builder
	content.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	content.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	rows := make([][]string, 0, len(data.rows)+1)
	rows = append(rows, data.headers)
	rows = append(rows, data.rows...)
	for rowIndex, row := range rows {
		fmt.Fprintf(&content, `<row r="%d">`, rowIndex+1)
		for columnIndex, value := range safeSpreadsheetRow(row) {
			reference := excelColumn(columnIndex+1) + fmt.Sprint(rowIndex+1)
			content.WriteString(`<c r="`)
			content.WriteString(reference)
			content.WriteString(`" t="inlineStr"><is><t xml:space="preserve">`)
			_ = xml.EscapeText(&content, []byte(value))
			content.WriteString(`</t></is></c>`)
		}
		content.WriteString(`</row>`)
	}
	content.WriteString(`</sheetData></worksheet>`)
	return content.String()
}

func excelColumn(index int) string {
	var result string
	for index > 0 {
		index--
		result = string(rune('A'+index%26)) + result
		index /= 26
	}
	return result
}

func safeSpreadsheetRow(row []string) []string {
	result := make([]string, len(row))
	for index, value := range row {
		result[index] = safeSpreadsheetCell(value)
	}
	return result
}

func safeSpreadsheetCell(value string) string {
	trimmed := strings.TrimLeft(value, " \v\f")
	if trimmed == "" {
		return value
	}
	switch trimmed[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + value
	default:
		return value
	}
}
