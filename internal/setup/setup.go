package setup

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/klortekhq/server-emus-ps5/internal/config"
)

var defaults = map[string][]string{
	"ps1": {".cue", ".chd", ".pbp", ".iso", ".ccd", ".toc", ".m3u", ".exe"},
	"ps2": {".iso", ".chd", ".cso", ".zso"},
	"dreamcast": {".gdi", ".cdi", ".chd", ".cue"},
	"saturn": {".cue", ".chd", ".ccd", ".mds"},
	"psp": {".iso", ".cso", ".pbp", ".chd"},
}

func Run(in io.Reader, out io.Writer, path string) error {
	scanner := bufio.NewScanner(in)
	cfg := config.Config{Listen: "0.0.0.0:8787"}

	fmt.Fprintln(out, "SERVER-EMUS-PS5 setup / configuración")
	fmt.Fprintln(out, "Press Enter / pulsa Enter on an empty system to finish.")

	for {
		system, err := prompt(scanner, out, "System / sistema (ps1, ps2, dreamcast...): ")
		if err != nil {
			return err
		}
		system = strings.ToLower(strings.TrimSpace(system))
		if system == "" {
			break
		}

		name, err := prompt(scanner, out, "Library name / nombre: ")
		if err != nil {
			return err
		}
		name = strings.TrimSpace(name)
		if name == "" {
			name = strings.ToUpper(system)
		}

		folder, err := prompt(scanner, out, "Folder / carpeta: ")
		if err != nil {
			return err
		}
		folder = strings.Trim(strings.TrimSpace(folder), "\"")
		abs, err := filepath.Abs(folder)
		if err != nil {
			fmt.Fprintf(out, "Invalid path / ruta inválida: %v\n", err)
			continue
		}
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			fmt.Fprintf(out, "Folder does not exist / la carpeta no existe: %s\n", abs)
			continue
		}

		recurseText, err := prompt(scanner, out, "Recursive / recursiva? [Y/n]: ")
		if err != nil {
			return err
		}
		recursive := true
		if strings.EqualFold(strings.TrimSpace(recurseText), "n") ||
			strings.EqualFold(strings.TrimSpace(recurseText), "no") {
			recursive = false
		}

		exts := append([]string(nil), defaults[system]...)
		if len(exts) != 0 {
			fmt.Fprintf(out, "Default extensions / extensiones: %s\n", strings.Join(exts, ", "))
		}

		cfg.Libraries = append(cfg.Libraries, config.Library{
			Name: name, System: system, Path: abs, Recursive: recursive, Extensions: exts,
		})
		fmt.Fprintln(out, "Added / añadida.")
	}

	if len(cfg.Libraries) == 0 {
		return fmt.Errorf("no libraries selected / no se seleccionaron bibliotecas")
	}

	port, err := prompt(scanner, out, "Port / puerto [8787]: ")
	if err != nil {
		return err
	}
	port = strings.TrimSpace(port)
	if port == "" {
		port = "8787"
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("invalid port / puerto inválido: %s", port)
	}
	cfg.Listen = "0.0.0.0:" + port

	token, err := prompt(scanner, out, "Optional token / token opcional (Enter = none): ")
	if err != nil {
		return err
	}
	cfg.Token = strings.TrimSpace(token)

	if err := config.Save(path, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "Saved / guardado: %s\n", path)
	return nil
}

func prompt(scanner *bufio.Scanner, out io.Writer, label string) (string, error) {
	fmt.Fprint(out, label)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", io.EOF
	}
	return scanner.Text(), nil
}
