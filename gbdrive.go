package main

import (
	"bytes"
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type Tool struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
	Ver  int    `yaml:"ver"`
}

type BranchCfg struct {
	Name     string `yaml:"name"`
	GdriveID string `yaml:"gdrive_id,omitempty"`
}

type RepoCfg struct {
	Folder   string      `yaml:"folder"`
	Name     string      `yaml:"name,omitempty"`
	Branches []BranchCfg `yaml:"branches"`
}

type Config struct {
	ParentGdriveID string    `yaml:"parent_gdrive_id,omitempty"`
	StoragePath    string    `yaml:"storage_path"`
	Password       string    `yaml:"password,omitempty"`
	Tools          []Tool    `yaml:"tools"`
	Repos          []RepoCfg `yaml:"repos"`
}

func loadConfig(path string) (*Config, error) {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func saveConfig(path string, c *Config) error {
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, b, 0644)
}

func findTool(cfg *Config, name string) (string, int) {
	for _, t := range cfg.Tools {
		if strings.EqualFold(t.Name, name) {
			p := strings.TrimSpace(t.Path)
			if p == "" {
				if runtime.GOOS == "windows" {
					p = name + ".exe"
				} else {
					p = name
				}
			}
			return p, t.Ver
		}
	}
	if runtime.GOOS == "windows" {
		return name + ".exe", 0
	}
	return name, 0
}

func sevenExecutableFromPath(p string) string {
	if p == "" {
		if runtime.GOOS == "windows" {
			return "7z.exe"
		}
		return "7z"
	}
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		if runtime.GOOS == "windows" {
			return filepath.Join(p, "7z.exe")
		}
		return filepath.Join(p, "7z")
	}
	return p
}

func gdriveExecutableFromPath(p string) string {
	if p == "" {
		if runtime.GOOS == "windows" {
			return "gdrive.exe"
		}
		return "gdrive"
	}
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		if runtime.GOOS == "windows" {
			return filepath.Join(p, "gdrive.exe")
		}
		return filepath.Join(p, "gdrive")
	}
	return p
}

func findGitRoot(start string) (string, error) {
	cur := start
	for {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", errors.New("git root not found")
		}
		cur = parent
	}
}

func getCurrentBranch(repoRoot string) (string, error) {
	cmd := exec.Command("git", "-C", repoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func repoConfigFor(cfg *Config, folder, branch string) (repoName string, repoIdx int, branchIdx int) {
	repoName = filepath.Base(folder)
	repoIdx = -1
	branchIdx = -1
	for i, r := range cfg.Repos {
		if strings.EqualFold(r.Folder, filepath.Base(folder)) {
			if r.Name != "" {
				repoName = r.Name
			}
			repoIdx = i
			for j, b := range r.Branches {
				if strings.TrimSpace(strings.TrimSuffix(b.Name, ":")) == branch {
					branchIdx = j
					return
				}
			}
			return
		}
	}
	return
}

func runCmdCapture(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	var errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%v: %s", err, errb.String())
	}
	return out.String(), nil
}

func gitPullFromBundle(repoRoot, bundleFile, branch string) error {
	_, err := runCmdCapture(repoRoot, "git", "pull", bundleFile, branch, "-t")
	return err
}

func gdriveUploadAndReturnID(gdriveExe string, ver int, archiveFullPath, parentID string) (string, error) {
	var args []string
	if ver == 2 {
		args = []string{"upload"}
	} else {
		args = []string{"files", "upload"}
	}
	if parentID != "" {
		args = append(args, "--parent", parentID)
	}
	if ver != 2 {
		args = append(args, "--print-only-id")
	}
	args = append(args, archiveFullPath)
	cmd := exec.Command(gdriveExe, args...)

	var out bytes.Buffer
	var errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, errb.String())
	}
	o := out.String()
	parts := strings.Fields(o)
	if len(parts) > 0 {
		return strings.Trim(parts[len(parts)-1], ")\n\r "), nil
	}
	return "", nil
}

func gdriveUpdateExisting(gdriveExe string, ver int, archiveFullPath, gdriveID string) error {
	var args []string
	if ver == 2 {
		args = []string{"update"}
	} else {
		args = []string{"files", "update"}
	}
	args = append(args, gdriveID, archiveFullPath)
	cmd := exec.Command(gdriveExe, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func gdriveDownload(gdriveExe string, ver int, destPath, gdriveID string) error {
	var args []string
	if ver == 2 {
		args = []string{"download", "--force", "--path"}
	} else {
		args = []string{"files", "download", "--overwrite", "--destination"}
	}
	args = append(args, destPath, gdriveID)
	cmd := exec.Command(gdriveExe, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func main() {
	log.SetFlags(0)

	// config path: same dir as executable
	exePath, err := os.Executable()
	if err != nil {
		log.Fatalf("executable path: %v", err)
	}
	exeDir := filepath.Dir(exePath)
	cfgPath := filepath.Join(exeDir, "gbdrive.yaml")

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	scfg := cfg
	storageCfgPath := filepath.Join(cfg.StoragePath, "gbdrive.yaml")
	if fi, err := os.Stat(storageCfgPath); err == nil && !fi.IsDir() {
		if scfg, err = loadConfig(storageCfgPath); err == nil {
			if scfg.ParentGdriveID != "" {
				cfg.ParentGdriveID = scfg.ParentGdriveID
			}
			if cfg.Password == "" {
				cfg.Password = scfg.Password
			}
			if len(scfg.Repos) > 0 {
				cfg.Repos = scfg.Repos
			}
		} else {
			log.Fatalf("failed to load storage config %s: %v", storageCfgPath, err)
		}
	} else {
		storageCfgPath = cfgPath
	}

	wd, _ := os.Getwd()
	repoRoot, err := findGitRoot(wd)
	if err != nil {
		log.Fatalf("find git root: %v", err)
	}
	branch, err := getCurrentBranch(repoRoot)
	if err != nil {
		log.Fatalf("get branch: %v", err)
	}

	repoName, repoIdx, branchIdx := repoConfigFor(cfg, repoRoot, branch)
	archiveName := fmt.Sprintf("%s.%s.7z", repoName, branch)
	archiveFull := filepath.Join(cfg.StoragePath, archiveName)

	sevenPathCfg, _ := findTool(cfg, "7z")
	sevenExe := sevenExecutableFromPath(sevenPathCfg)

	gdrivePathCfg, gdriveVer := findTool(cfg, "gdrive")
	gdriveExe := gdriveExecutableFromPath(gdrivePathCfg)

	args := os.Args[1:]
	if len(args) == 0 {
		// no args: extract and pull
		if _, err := os.Stat(archiveFull); os.IsNotExist(err) {
			log.Fatalf("archive not found: %s", archiveFull)
		}
		tmpBundle := filepath.Join(os.TempDir(), repoName+"."+branch+".bundle")
		sevCmd := exec.Command(sevenExe, "e", "-p"+cfg.Password, archiveFull, "-so")
		outFile, err := os.Create(tmpBundle)
		if err != nil {
			log.Fatalf("create tmp bundle: %v", err)
		}
		sevCmd.Stdout = outFile
		sevCmd.Stderr = os.Stderr
		if err := sevCmd.Run(); err != nil {
			outFile.Close()
			log.Fatalf("7z extract failed: %v", err)
		}
		outFile.Close()
		if err := gitPullFromBundle(repoRoot, tmpBundle, branch); err != nil {
			log.Fatalf("git pull from bundle failed: %v", err)
		}
		_ = os.Remove(tmpBundle)
		log.Println("pulled from bundle")
		return
	}

	// up/dn/up new handling
	switch args[0] {
	case "up":
		// up or up new
		if _, err := os.Stat(archiveFull); os.IsNotExist(err) {
			log.Fatalf("archive not found: %s", archiveFull)
		}
		if len(args) >= 2 && args[1] == "new" {
			parentID := cfg.ParentGdriveID
			// upload and obtain ID
			id, err := gdriveUploadAndReturnID(gdriveExe, gdriveVer, archiveFull, parentID)
			if err != nil {
				log.Fatalf("gdrive upload failed: %v", err)
			}
			if id == "" {
				log.Fatalf("gdrive upload did not return an ID")
			}
			// write id into config for repo/branch, adding repo/branch if missing
			if repoIdx == -1 {
				// add repo entry
				newRepo := RepoCfg{Folder: filepath.Base(repoRoot)}
				scfg.Repos = append(scfg.Repos, newRepo)
				repoIdx = len(scfg.Repos) - 1
			}
			// ensure branch exists
			if branchIdx == -1 {
				scfg.Repos[repoIdx].Branches = append(scfg.Repos[repoIdx].Branches, BranchCfg{Name: branch, GdriveID: id})
			} else {
				scfg.Repos[repoIdx].Branches[branchIdx].GdriveID = id
			}
			if err := saveConfig(storageCfgPath, scfg); err != nil {
				log.Fatalf("save config failed: %v", err)
			}
			log.Printf("uploaded and saved gdrive_id %s", id)
			return
		}

		// plain up: upload existing archive; require gdrive_id in config
		if repoIdx == -1 || branchIdx == -1 || cfg.Repos[repoIdx].Branches[branchIdx].GdriveID == "" {
			log.Fatalf("gdrive_id not configured for branch %s", branch)
		}
		gid := cfg.Repos[repoIdx].Branches[branchIdx].GdriveID
		if err := gdriveUpdateExisting(gdriveExe, gdriveVer, archiveFull, gid); err != nil {
			log.Fatalf("gdrive update failed: %v", err)
		}
		log.Println("updated")
		return

	case "dn":
		// download archive
		if repoIdx == -1 || branchIdx == -1 || cfg.Repos[repoIdx].Branches[branchIdx].GdriveID == "" {
			log.Fatalf("gdrive_id not configured for branch %s", branch)
		}
		gid := cfg.Repos[repoIdx].Branches[branchIdx].GdriveID
		if err := gdriveDownload(gdriveExe, gdriveVer, cfg.StoragePath, gid); err != nil {
			log.Fatalf("gdrive download failed: %v", err)
		}
		log.Println("downloaded")
		return
	}

	// numeric days case, optional tag as second arg
	if days, err := strconv.Atoi(args[0]); err == nil {
		_ = os.Remove(archiveFull)
		sinceArg := fmt.Sprintf("--since=%d days ago", days)
		gitArgs := []string{"bundle", "create", "-", sinceArg, branch}
		if len(args) > 1 && args[1] != "" {
			gitArgs = append(gitArgs, args[1])
		}
		gitCmd := exec.Command("git", gitArgs...)
		gitCmd.Dir = repoRoot
		gitStdout, err := gitCmd.StdoutPipe()
		if err != nil {
			log.Fatalf("git stdout pipe: %v", err)
		}
		gitCmd.Stderr = os.Stderr
		sevCmd := exec.Command(sevenExe, "a", "-p"+cfg.Password, "-si", "-mx=5", archiveFull)
		sevCmd.Stdin = gitStdout
		sevCmd.Stdout = os.Stdout
		sevCmd.Stderr = os.Stderr

		if err := gitCmd.Start(); err != nil {
			log.Fatalf("start git bundle: %v", err)
		}
		if err := sevCmd.Start(); err != nil {
			_ = gitCmd.Process.Kill()
			log.Fatalf("start 7z: %v", err)
		}
		if err := gitCmd.Wait(); err != nil {
			_ = sevCmd.Wait()
			log.Fatalf("git bundle failed: %v", err)
		}
		if err := sevCmd.Wait(); err != nil {
			log.Fatalf("7z packing failed: %v", err)
		}
		log.Printf("bundle created and packed to %s", archiveFull)
		return
	}

	log.Fatalf("unknown argument: %s", args[0])
}
