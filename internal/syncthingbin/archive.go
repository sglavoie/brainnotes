package syncthingbin

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"
)

// maxBinary bounds what will be read out of an archive. The archive itself was
// already matched against a recorded checksum, so this is not a defence against
// a hostile file; it is a defence against a future release whose layout changed
// under a name this code still recognises.
const maxBinary = 256 << 20

// unpack returns the syncthing executable from a verified release archive.
//
// The member is addressed exactly, as "<archive stem>/syncthing", rather than by
// searching for a file named syncthing. The Linux archives also ship
// etc/linux-systemd/... and etc/firewall-ufw/... entries, and at least one of
// them ends in a file called syncthing that is a service unit rather than a
// program; a search would find whichever came first in the archive.
func unpack(asset string, archive []byte) ([]byte, error) {
	if strings.HasSuffix(asset, ".zip") {
		return fromZip(strings.TrimSuffix(asset, ".zip")+"/syncthing", archive)
	}
	return fromTarGz(strings.TrimSuffix(asset, ".tar.gz")+"/syncthing", archive)
}

func fromZip(member string, archive []byte) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("syncthingbin: open zip: %w", err)
	}
	for _, entry := range reader.File {
		if entry.Name != member {
			continue
		}
		body, err := entry.Open()
		if err != nil {
			return nil, fmt.Errorf("syncthingbin: open %s: %w", member, err)
		}
		defer func() { _ = body.Close() }()
		return readAll(member, body)
	}
	return nil, fmt.Errorf("syncthingbin: %s: %w", member, errNoBinary)
}

func fromTarGz(member string, archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("syncthingbin: open gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("syncthingbin: %s: %w", member, errNoBinary)
		}
		if err != nil {
			return nil, fmt.Errorf("syncthingbin: read tar: %w", err)
		}
		if header.Name != member || header.Typeflag != tar.TypeReg {
			continue
		}
		return readAll(member, reader)
	}
}

func readAll(member string, source io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(source, maxBinary+1))
	if err != nil {
		return nil, fmt.Errorf("syncthingbin: read %s: %w", member, err)
	}
	if len(body) > maxBinary {
		return nil, fmt.Errorf("syncthingbin: %s exceeds %d bytes", member, maxBinary)
	}
	return body, nil
}
