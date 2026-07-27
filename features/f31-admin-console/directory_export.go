package adminconsole

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type DirectoryExport struct {
	Body        []byte
	ContentType string
	Filename    string
}

func (service *Service) ExportUsers(
	ctx context.Context,
	query UserDirectoryQuery,
	format string,
) (DirectoryExport, error) {
	normalized, err := normalizeUserDirectoryQuery(query)
	if err != nil {
		return DirectoryExport{}, err
	}
	normalized.Page = 1
	normalized.PageSize = MaxDirectoryPageSize
	result, err := service.reader.ListUsers(ctx, normalized)
	if err != nil {
		return DirectoryExport{}, errors.Join(ErrAdministrationUnavailable, err)
	}
	if result.Total > AdminExportLimit {
		return DirectoryExport{}, ErrAdminExportTooLarge
	}
	items := result.Items
	for len(items) < result.Total {
		normalized.Page++
		page, pageErr := service.reader.ListUsers(ctx, normalized)
		if pageErr != nil {
			return DirectoryExport{}, errors.Join(ErrAdministrationUnavailable, pageErr)
		}
		if len(page.Items) == 0 {
			return DirectoryExport{}, ErrAdministrationUnavailable
		}
		items = append(items, page.Items...)
	}
	rows := userExportRows(items)
	return buildDirectoryExport("users", rows, format, service.now().UTC())
}

func (service *Service) ExportWorkspaces(
	ctx context.Context,
	query WorkspaceDirectoryQuery,
	format string,
) (DirectoryExport, error) {
	normalized, err := normalizeWorkspaceDirectoryQuery(query)
	if err != nil {
		return DirectoryExport{}, err
	}
	normalized.Page = 1
	normalized.PageSize = MaxDirectoryPageSize
	result, err := service.reader.ListWorkspaces(ctx, normalized)
	if err != nil {
		return DirectoryExport{}, errors.Join(ErrAdministrationUnavailable, err)
	}
	if result.Total > AdminExportLimit {
		return DirectoryExport{}, ErrAdminExportTooLarge
	}
	items := result.Items
	for len(items) < result.Total {
		normalized.Page++
		page, pageErr := service.reader.ListWorkspaces(ctx, normalized)
		if pageErr != nil {
			return DirectoryExport{}, errors.Join(ErrAdministrationUnavailable, pageErr)
		}
		if len(page.Items) == 0 {
			return DirectoryExport{}, ErrAdministrationUnavailable
		}
		items = append(items, page.Items...)
	}
	rows := workspaceExportRows(items)
	return buildDirectoryExport("workspaces", rows, format, service.now().UTC())
}

func userExportRows(items []UserDirectoryItem) [][]string {
	rows := [][]string{{
		"Email", "Name", "Account status", "Email verified", "Login methods",
		"Registered at", "Last login", "Active sessions", "Workspaces",
	}}
	for _, item := range items {
		workspaces := make([]string, 0, len(item.Workspaces))
		for _, workspace := range item.Workspaces {
			workspaces = append(workspaces, fmt.Sprintf(
				"%s (%s, %s, %s)",
				workspace.Name,
				workspace.Role,
				workspace.PlanCode,
				workspace.PlanStatus,
			))
		}
		lastLogin := ""
		if item.LastLoginAt != nil {
			lastLogin = item.LastLoginAt.UTC().Format(time.RFC3339)
		}
		rows = append(rows, []string{
			item.Email,
			item.DisplayName,
			item.AccountStatus,
			strconv.FormatBool(item.EmailVerified),
			strings.Join(item.LoginMethods, ", "),
			item.RegisteredAt.UTC().Format(time.RFC3339),
			lastLogin,
			strconv.Itoa(item.ActiveSessions),
			strings.Join(workspaces, "; "),
		})
	}
	return rows
}

func workspaceExportRows(items []WorkspaceDirectoryItem) [][]string {
	rows := [][]string{{
		"Name", "Owner email", "Owner name", "Status", "Plan", "Plan status",
		"Members", "Channels", "Posts", "Created at", "Updated at",
	}}
	for _, item := range items {
		rows = append(rows, []string{
			item.Name,
			item.OwnerEmail,
			item.OwnerDisplayName,
			item.Status,
			item.PlanCode,
			item.PlanStatus,
			strconv.Itoa(item.MemberCount),
			strconv.Itoa(item.ChannelCount),
			strconv.Itoa(item.PostCount),
			item.CreatedAt.UTC().Format(time.RFC3339),
			item.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	return rows
}

func buildDirectoryExport(
	subject string,
	rows [][]string,
	format string,
	now time.Time,
) (DirectoryExport, error) {
	filename := "postqron-admin-" + subject + "-" + now.Format("20060102")
	switch format {
	case "csv":
		body, err := encodeCSV(rows)
		return DirectoryExport{
			Body: body, ContentType: "text/csv; charset=utf-8",
			Filename: filename + ".csv",
		}, err
	case "xlsx":
		body, err := encodeXLSX(subject, rows)
		return DirectoryExport{
			Body:        body,
			ContentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
			Filename:    filename + ".xlsx",
		}, err
	default:
		return DirectoryExport{}, ErrInvalidRequest
	}
}

func encodeCSV(rows [][]string) ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteString("\xEF\xBB\xBF")
	writer := csv.NewWriter(&buffer)
	for _, row := range rows {
		safe := make([]string, len(row))
		for index, value := range row {
			safe[index] = spreadsheetSafe(value)
		}
		if err := writer.Write(safe); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	return buffer.Bytes(), writer.Error()
}

func encodeXLSX(sheetName string, rows [][]string) ([]byte, error) {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
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
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
</Relationships>`,
		"xl/workbook.xml": fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<sheets><sheet name="%s" sheetId="1" r:id="rId1"/></sheets>
</workbook>`, xmlEscape(sheetName)),
		"xl/worksheets/sheet1.xml": worksheetXML(rows),
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
	return buffer.Bytes(), nil
}

func worksheetXML(rows [][]string) string {
	var output strings.Builder
	output.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	output.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for rowIndex, row := range rows {
		output.WriteString(`<row r="`)
		output.WriteString(strconv.Itoa(rowIndex + 1))
		output.WriteString(`">`)
		for columnIndex, value := range row {
			output.WriteString(`<c r="`)
			output.WriteString(cellReference(columnIndex, rowIndex))
			output.WriteString(`" t="inlineStr"><is><t xml:space="preserve">`)
			output.WriteString(xmlEscape(spreadsheetSafe(value)))
			output.WriteString(`</t></is></c>`)
		}
		output.WriteString(`</row>`)
	}
	output.WriteString(`</sheetData></worksheet>`)
	return output.String()
}

func spreadsheetSafe(value string) string {
	trimmed := strings.TrimLeft(value, " ")
	if trimmed != "" && strings.ContainsRune("=+-@\t\r", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}

func cellReference(column, row int) string {
	column++
	var letters string
	for column > 0 {
		column--
		letters = string(rune('A'+column%26)) + letters
		column /= 26
	}
	return letters + strconv.Itoa(row+1)
}

func xmlEscape(value string) string {
	var buffer bytes.Buffer
	_ = xml.EscapeText(&buffer, []byte(value))
	return buffer.String()
}
