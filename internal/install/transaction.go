package install

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type change struct {
	Path          string `json:"path"`
	Before, After []byte
	Mode          uint32
	BeforeMode    *uint32 `json:"before_mode,omitempty"`
}
type transaction struct {
	Format      int `json:"format,omitempty"`
	Destination string
	Changes     []change
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if e := safeFile(path); e != nil {
		return e
	}
	if data == nil {
		e := os.Remove(path)
		if os.IsNotExist(e) {
			return nil
		}
		return e
	}
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".denmother-stage-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func apply(op *Operation, action string) error { return applyContext(context.Background(), op, action) }
func applyContext(ctx context.Context, op *Operation, action string) error {
	if err := ctx.Err(); err != nil {
		return interrupted("installation canceled: %w", err)
	}
	tx := transaction{Format: 1, Destination: op.Destination}
	desired := map[string][]byte{}
	for p, b := range op.files {
		desired[p] = b
	}
	if action == "uninstall" {
		desired = map[string][]byte{}
	}
	if op.old != nil {
		for p := range op.old.Files {
			if _, ok := desired[p]; !ok {
				desired[p] = nil
			}
		}
	}
	var remaining = map[string]string{}
	snapshotBytes := 0
	for _, p := range sortedKeys(desired) {
		path := filepath.Join(op.Destination, filepath.FromSlash(p))
		before, e := readRegular(path)
		if os.IsNotExist(e) {
			before = nil
		} else if e != nil {
			if action == "uninstall" {
				op.Leftovers = append(op.Leftovers, path)
				remaining[p] = op.old.Files[p]
				continue
			}
			return e
		}
		if action == "install" && before != nil && (op.old == nil || digest(before) != op.old.Files[p]) {
			return fmt.Errorf("destination changed after planning: %s; inspect local edits before retrying", path)
		}
		if action == "uninstall" && before != nil && digest(before) != op.old.Files[p] {
			op.Leftovers = append(op.Leftovers, path)
			remaining[p] = op.old.Files[p]
			continue
		}
		if sameContent(before, desired[p]) {
			continue
		}
		mode := uint32(0644)
		if op.Kind == "binary" {
			mode = 0755
		}
		snapshotBytes += len(before) + len(desired[p])
		if snapshotBytes > 2*maxManagedFileSize {
			return fmt.Errorf("transaction snapshots exceed size limit")
		}
		var beforeMode *uint32
		if before != nil {
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			value := uint32(info.Mode().Perm())
			beforeMode = &value
		}
		tx.Changes = append(tx.Changes, change{Path: p, Before: before, After: desired[p], Mode: mode, BeforeMode: beforeMode})
	}
	var manifest []byte
	if action == "uninstall" {
		if len(remaining) > 0 {
			m := *op.old
			m.Files = remaining
			hashes, _ := json.Marshal(remaining)
			m.Digest = digest(hashes)
			manifest, _ = json.MarshalIndent(m, "", "  ")
			op.Manifest = &m
		} else {
			op.Manifest = nil
		}
	} else {
		manifest, _ = json.MarshalIndent(op.Manifest, "", "  ")
	}
	before, e := readRegularLimit(op.manifestPath, maxManifestSize)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if !sameContent(before, op.oldBytes) {
		return fmt.Errorf("ownership manifest changed after planning: %s", op.manifestPath)
	}
	var beforeMode *uint32
	if before != nil {
		info, err := os.Lstat(op.manifestPath)
		if err != nil {
			return err
		}
		value := uint32(info.Mode().Perm())
		beforeMode = &value
	}
	tx.Changes = append(tx.Changes, change{Path: filepath.Base(op.manifestPath), Before: before, After: manifest, Mode: 0644, BeforeMode: beforeMode})
	if _, _, err := validateTransaction(op, &tx); err != nil {
		return err
	}
	data, e := json.Marshal(tx)
	if e != nil {
		return e
	}
	if len(data) > maxJournalSize {
		return fmt.Errorf("recovery journal exceeds size limit")
	}
	journal := op.manifestPath + ".transaction"
	if _, err := os.Lstat(journal); !os.IsNotExist(err) {
		return fmt.Errorf("recovery journal appeared after planning: %s", journal)
	}
	if e := atomicWrite(journal, data, 0600); e != nil {
		return e
	}
	op.State = "applying"
	op.AppliedFiles = []string{}
	for _, c := range tx.Changes {
		op.RemainingFiles = append(op.RemainingFiles, filepath.Join(op.Destination, c.Path))
	}
	for _, c := range tx.Changes {
		if err := ctx.Err(); err != nil {
			return interrupted("installation canceled: %w", err)
		}
		current, err := readRegular(filepath.Join(op.Destination, c.Path))
		if err != nil && !os.IsNotExist(err) {
			return interrupted("interrupted installation: %w", err)
		}
		if !sameContent(current, c.Before) {
			return interrupted("interrupted installation: destination changed after staging: %s; preserve edits before --recover", c.Path)
		}
		if e := atomicWrite(filepath.Join(op.Destination, c.Path), c.After, os.FileMode(c.Mode)); e != nil {
			return interrupted("interrupted installation: %w; journal %s; retry with --recover", e, journal)
		}
		op.AppliedFiles = append(op.AppliedFiles, filepath.Join(op.Destination, c.Path))
		op.RemainingFiles = op.RemainingFiles[1:]
	}
	op.State = "applied"
	if e := os.Remove(journal); e != nil {
		return interrupted("interrupted installation: %w", e)
	}
	if action == "uninstall" && op.Kind == "skill" {
		// Remove empty directories only; unrelated files, links, and edits survive.
		dirs := map[string]bool{}
		for p := range op.old.Files {
			for d := filepath.Dir(filepath.Join(op.Destination, p)); d != op.Destination && len(d) > len(op.Destination); d = filepath.Dir(d) {
				dirs[d] = true
			}
		}
		paths := sortedKeys(dirs)
		sort.Slice(paths, func(i, j int) bool { return len(paths[i]) > len(paths[j]) })
		for _, d := range append(paths, op.Destination) {
			if info, err := os.Lstat(d); err == nil && info.IsDir() && safeFile(d) == nil {
				_ = os.Remove(d)
			}
		}
		if entries, e := os.ReadDir(op.Destination); e == nil {
			for _, entry := range entries {
				p := filepath.Join(op.Destination, entry.Name())
				found := false
				for _, existing := range op.Leftovers {
					if existing == p {
						found = true
					}
				}
				if !found {
					op.Leftovers = append(op.Leftovers, p)
				}
			}
		}
	}
	return nil
}

// A missing file and an existing empty file are different ownership states.
func sameContent(a, b []byte) bool { return (a == nil) == (b == nil) && bytes.Equal(a, b) }

func validateTransaction(op *Operation, tx *transaction) (*Manifest, *Manifest, error) {
	if tx.Format != 0 && tx.Format != 1 || tx.Destination != op.Destination || len(tx.Changes) < 1 || len(tx.Changes) > maxManagedFiles+1 {
		return nil, nil, fmt.Errorf("invalid recovery transaction")
	}
	last := tx.Changes[len(tx.Changes)-1]
	if last.Path != filepath.Base(op.manifestPath) {
		return nil, nil, fmt.Errorf("recovery journal must end with its ownership manifest")
	}
	var before, after *Manifest
	var err error
	if last.Before != nil {
		before, err = decodeManifest(last.Before, op.Destination, op.Kind)
		if err != nil {
			return nil, nil, err
		}
	}
	if last.After != nil {
		after, err = decodeManifest(last.After, op.Destination, op.Kind)
		if err != nil {
			return nil, nil, err
		}
	}
	if before == nil && after == nil {
		return nil, nil, fmt.Errorf("recovery journal has no ownership metadata")
	}
	if before != nil && after != nil && (before.ID != after.ID || before.Scope != after.Scope) {
		return nil, nil, fmt.Errorf("recovery journal changes ownership identity")
	}
	seen := map[string]bool{}
	for i, c := range tx.Changes {
		if seen[c.Path] {
			return nil, nil, fmt.Errorf("duplicate recovery path")
		}
		seen[c.Path] = true
		expectedMode := uint32(0644)
		if op.Kind == "binary" && c.Path == binaryName() {
			expectedMode = 0755
		}
		if c.Mode != expectedMode || c.BeforeMode != nil && *c.BeforeMode & ^uint32(0777) != 0 {
			return nil, nil, fmt.Errorf("unsafe recovery mode")
		}
		if len(c.Before) > maxManagedFileSize || len(c.After) > maxManagedFileSize {
			return nil, nil, fmt.Errorf("recovery payload exceeds size limit")
		}
		if i == len(tx.Changes)-1 {
			continue
		}
		if !managedPath(c.Path) || op.Kind == "binary" && c.Path != binaryName() {
			return nil, nil, fmt.Errorf("unsafe recovery path")
		}
		oldHash, newHash := "", ""
		if before != nil {
			oldHash = before.Files[c.Path]
		}
		if after != nil {
			newHash = after.Files[c.Path]
		}
		if oldHash == "" && newHash == "" {
			return nil, nil, fmt.Errorf("recovery path is not owned: %s", c.Path)
		}
		if c.Before != nil && (oldHash == "" || digest(c.Before) != oldHash) {
			return nil, nil, fmt.Errorf("recovery before-content does not match ownership: %s", c.Path)
		}
		if c.After == nil && newHash != "" || c.After != nil && (newHash == "" || digest(c.After) != newHash) {
			return nil, nil, fmt.Errorf("recovery after-content does not match ownership: %s", c.Path)
		}
	}
	return before, after, nil
}

func recoverTransaction(op *Operation) error {
	return recoverTransactionContext(context.Background(), op)
}
func recoverTransactionContext(ctx context.Context, op *Operation) error {
	if err := ctx.Err(); err != nil {
		return interrupted("recovery canceled: %w", err)
	}
	journal := op.manifestPath + ".transaction"
	data, err := readRegularLimit(journal, maxJournalSize)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var tx transaction
	if err := decodeMetadata(data, &tx); err != nil {
		return fmt.Errorf("invalid recovery journal: %w", err)
	}
	before, _, err := validateTransaction(op, &tx)
	if err != nil {
		return err
	}
	// Check the entire rollback before changing any file, including the manifest.
	for _, c := range tx.Changes {
		if err := ctx.Err(); err != nil {
			return interrupted("recovery canceled: %w", err)
		}
		p := filepath.Join(op.Destination, c.Path)
		now, err := readRegular(p)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if !sameContent(now, c.Before) && !sameContent(now, c.After) {
			return fmt.Errorf("recovery preserves local modification: %s; back up this file before repairing the journal", p)
		}
	}
	op.Recovery = &RecoveryReport{State: "restoring", RestoredFiles: []string{}, SkippedFiles: []string{}, RemainingFiles: []string{}}
	for _, c := range tx.Changes {
		op.Recovery.RemainingFiles = append(op.Recovery.RemainingFiles, filepath.Join(op.Destination, c.Path))
	}
	// Restore payload first, then ownership metadata. A crash leaves the same
	// journal valid and the previous manifest is never advertised prematurely.
	for _, c := range tx.Changes {
		if err := ctx.Err(); err != nil {
			return interrupted("recovery canceled: %w", err)
		}
		p := filepath.Join(op.Destination, c.Path)
		now, err := readRegular(p)
		if err != nil && !os.IsNotExist(err) {
			return interrupted("interrupted recovery: %w", err)
		}
		if !sameContent(now, c.Before) && !sameContent(now, c.After) {
			return interrupted("interrupted recovery preserves local modification: %s", p)
		}
		mode := c.Mode
		if c.BeforeMode != nil {
			mode = *c.BeforeMode
		}
		alreadyRestored := sameContent(now, c.Before)
		if alreadyRestored && c.Before != nil && c.BeforeMode != nil {
			info, err := os.Stat(p)
			alreadyRestored = err == nil && uint32(info.Mode().Perm()) == mode
		}
		if alreadyRestored {
			op.Recovery.SkippedFiles = append(op.Recovery.SkippedFiles, p)
		} else {
			if err := atomicWrite(p, c.Before, os.FileMode(mode)); err != nil {
				return interrupted("interrupted recovery: %w", err)
			}
			op.Recovery.RestoredFiles = append(op.Recovery.RestoredFiles, p)
		}
		op.Recovery.RemainingFiles = op.Recovery.RemainingFiles[1:]
	}
	if err := os.Remove(journal); err != nil {
		return interrupted("interrupted recovery: %w", err)
	}
	if before == nil && op.Kind == "skill" {
		removeEmptyTransactionDirs(op.Destination, tx.Changes)
	}
	op.Recovery.State = "restored"
	return nil
}

func removeEmptyTransactionDirs(destination string, changes []change) {
	dirs := map[string]bool{destination: true}
	for _, c := range changes {
		for d := filepath.Dir(filepath.Join(destination, c.Path)); len(d) > len(destination); d = filepath.Dir(d) {
			dirs[d] = true
		}
	}
	paths := sortedKeys(dirs)
	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) > len(paths[j]) })
	for _, d := range paths {
		if info, err := os.Lstat(d); err == nil && info.IsDir() && safeFile(d) == nil {
			_ = os.Remove(d)
		}
	}
}
