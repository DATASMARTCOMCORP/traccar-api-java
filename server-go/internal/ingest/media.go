package ingest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WriteMedia is MediaManager.writeFile: <media.path>/<uniqueId>/<yyyyMMddHHmmss>.<ext>,
// the time in the server's zone. Failures are logged and give "" (Java's null).
func (c *Conn) WriteMedia(uniqueID, extension string, data []byte) string {
	name, err := writeMedia(c.p.cfg.String("media.path", "./media"), uniqueID,
		time.Now().In(c.p.st.Location()).Format("20060102150405")+"."+extension, data)
	if err != nil {
		c.p.log.Warn("save media file error", "error", err)
		return ""
	}
	return name
}

func writeMedia(root, uniqueID, name string, data []byte) (string, error) {
	base, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	base = filepath.Clean(base)
	if filepath.IsAbs(uniqueID) || filepath.IsAbs(name) {
		// Path.resolve takes an absolute path as it is: outside the media path.
		return "", errors.New("invalid media path")
	}
	path := filepath.Clean(filepath.Join(base, uniqueID, name))
	if path != base && !strings.HasPrefix(path, base+string(filepath.Separator)) {
		return "", errors.New("invalid media path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return "", err
	}
	return name, f.Close()
}
