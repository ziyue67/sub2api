package service

import "github.com/Wei-Shaw/sub2api/internal/service/basispoints"

const ExcelBPSOmitUnsupportedToolsKey = "openai_excel_bps_omit_unsupported_tools"

// IsExcelBPSOmitUnsupportedToolsEnabled opts into the bridge's hosted-tool
// omission and capability notice. Forced selections still fail validation.
func (a *Account) IsExcelBPSOmitUnsupportedToolsEnabled() bool {
	return a.IsExcelBPSEnabled() && a.Extra[ExcelBPSOmitUnsupportedToolsKey] == true
}

func (a *Account) excelBPSNativeFallbackReason(body []byte) string {
	if a.IsExcelBPSOmitUnsupportedToolsEnabled() {
		return ""
	}
	return basispoints.NativeFallbackReason(body)
}
