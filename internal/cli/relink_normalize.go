package cli

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/sglavoie/brainnotes/internal/textnorm"
	"github.com/sglavoie/brainnotes/internal/wikilink"
)

// relinkMediaExts names the embed extensions relink never rewrites: an
// unresolved `![[diagram.png]]` is an asset reference, not a broken note link.
var relinkMediaExts = map[string]struct{}{
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".svg": {}, ".webp": {},
	".pdf": {}, ".mp4": {}, ".mov": {}, ".mp3": {}, ".wav": {}, ".ogg": {}, ".heic": {},
}

func relinkNormalizeTarget(target string) string {
	target = strings.TrimSpace(strings.ReplaceAll(target, "\\", "/"))
	target = strings.TrimPrefix(path.Clean("/"+target), "/")
	target = relinkStripMDExt(target)
	if target == "" || target == "." {
		return ""
	}
	return strings.ToLower(textnorm.NFC(target))
}

func relinkNormalizeTitle(title string) string {
	title = strings.TrimSpace(title)
	title = relinkStripMDExt(title)
	if title == "" {
		return ""
	}
	return strings.ToLower(textnorm.NFC(title))
}

func relinkStripMDExt(s string) string {
	base, _ := relinkSplitMDExt(s)
	return base
}

func relinkTargetTitle(target string) string {
	target = strings.TrimSpace(target)
	if idx := strings.LastIndexAny(target, `/\`); idx >= 0 {
		return target[idx+1:]
	}
	return target
}

func relinkSplitMDExt(s string) (string, string) {
	if len(s) >= 3 && strings.EqualFold(s[len(s)-3:], ".md") {
		return s[:len(s)-3], s[len(s)-3:]
	}
	return s, ""
}

func relinkIsMediaEmbed(title string) bool {
	ext := strings.ToLower(filepath.Ext(title))
	_, ok := relinkMediaExts[ext]
	return ok
}

func relinkRenderTarget(link wikilink.Link, target relinkTarget) string {
	out := target.title
	if strings.ContainsAny(link.Target, `/\`) {
		out = target.relNoExt
	}
	_, ext := relinkSplitMDExt(relinkTargetTitle(link.Target))
	if ext != "" {
		out += ext
	}
	return out
}
