package intake

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// extMIME is a fixed table, so results do not depend on the host OS registry.
var extMIME = map[string]string{
	".pdf":  "application/pdf",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
	".tif":  "image/tiff",
	".tiff": "image/tiff",
	".heic": "image/heic",
	".txt":  "text/plain",
	".md":   "text/markdown",
	".csv":  "text/csv",
	".html": "text/html",
	".htm":  "text/html",
	".xml":  "text/xml",
	".json": "application/json",
	".rtf":  "application/rtf",
	".doc":  "application/msword",
	".xls":  "application/vnd.ms-excel",
	".ppt":  "application/vnd.ms-powerpoint",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".odt":  "application/vnd.oasis.opendocument.text",
	".ods":  "application/vnd.oasis.opendocument.spreadsheet",
	".zip":  "application/zip",
	".djvu": "image/vnd.djvu",
	".eml":  "message/rfc822",
}

// ExtMIME returns the type implied by the file name, or "".
func ExtMIME(name string) string {
	return extMIME[strings.ToLower(filepath.Ext(name))]
}

// containerTypes are generic content types that a known extension may refine:
// an .docx is a zip by content, an .doc is an OLE container.
var containerTypes = map[string]bool{
	"application/zip":           true,
	"application/octet-stream":  true,
	"application/x-ole-storage": true,
}

// ContentMIME sniffs the type from the first bytes of the file and refines a
// generic container type by the extension.
func ContentMIME(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", err
	}
	m := http.DetectContentType(buf[:n])
	if i := strings.IndexByte(m, ';'); i >= 0 {
		m = m[:i]
	}
	if containerTypes[m] {
		if e := ExtMIME(path); e != "" && e != "application/zip" {
			if m == "application/octet-stream" || isZipFamily(e) {
				return e, nil
			}
		}
	}
	return m, nil
}

func isZipFamily(m string) bool {
	return strings.Contains(m, "openxmlformats") || strings.Contains(m, "opendocument")
}
