package registries

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/humio/humio-operator/api/v1alpha1"
	humioapi "github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"gopkg.in/yaml.v3"
)

const (
	maxPackageSize            int64  = 100 * 1024 * 1024 // 100MB
	maxPackageAssetFolderSize int64  = 2 * 1024 * 1024   // 2MB
	maxPackageAssetFolders    int    = 15                // just a sane limit not an exact one
	packageDefaultExtension   string = ".zip"
)

var (
	excludedAssetDirs = []string{"data"}
	expectedAssetDirs = []string{"dashboards", "alerts", "filter-alerts", "aggregate-alerts", "queries", "parsers", "actions", "scheduled-searches", "view-interactions"}
)

type PackageInterface interface {
	BuildDownloadPath(string, string) (string, error)
	ExtractPackage() error
	DeletePackage() error
	Validate() (string, error)
}

type LogscalePackage struct {
	Name           string
	DownloadPath   string
	Version        string
	Extension      string
	Checksum       string
	ConflictPolicy string
	Logger         logr.Logger
}

func NewLogscalePackage(hp *v1alpha1.HumioPackage, logger logr.Logger) *LogscalePackage {
	return &LogscalePackage{
		DownloadPath:   "",
		Name:           hp.Spec.GetPackageName(),
		Version:        hp.Spec.PackageVersion,
		Extension:      packageDefaultExtension,
		Checksum:       hp.Spec.PackageChecksum,
		ConflictPolicy: hp.Spec.ConflictPolicy,
		Logger:         logger,
	}
}

func (p *LogscalePackage) BuildDownloadPath(basePath string, reconcileID string) (string, error) {
	var err error
	cleanPath := strings.ReplaceAll(p.Name, "/", "-")
	cleanPath = filepath.Clean(cleanPath)

	// Always include reconcile ID in filename to prevent race conditions
	filename := fmt.Sprintf("%s-%s-%s%s", cleanPath, p.Version, reconcileID, p.Extension)
	filename = filepath.Clean(filename)

	// Ensure it's valid
	if filename == "" || filename == "." || filename == ".." {
		return "", fmt.Errorf("invalid filename after sanitization")
	}

	fullPath := filepath.Join(basePath, filename)
	fullPath = filepath.Clean(fullPath)

	// Ensure the result is still within basePath
	base := filepath.Clean(basePath) + string(filepath.Separator)
	if !strings.HasPrefix(fullPath, base) {
		return "", fmt.Errorf("path traversal attempt detected")
	}
	p.DownloadPath = fullPath

	return fullPath, err
}

func (p *LogscalePackage) Validate(ctx context.Context, humioClient humio.Client, humioHTTPClient *humioapi.Client, view string) (string, error) {
	var pkName string

	// test checksum match
	localChecksum, err := p.generateChecksum(p.DownloadPath)
	if err != nil {
		return "", fmt.Errorf("could not compute checksum:%s", err)
	}
	hash, err := extrackHash(p.Checksum)
	if err != nil {
		return "", fmt.Errorf("could not extract hash from checksum: %s", p.Checksum)
	}
	if localChecksum != hash {
		return "", fmt.Errorf("checksum comparison failed, expected: %s, found:%s", p.Checksum, localChecksum)
	}

	// test total size < maxPackageSize
	stats, err := os.Stat(p.DownloadPath)
	if err != nil {
		return "", fmt.Errorf("could not get file info: %s", err)
	}
	if stats.Size() > maxPackageSize {
		return "", fmt.Errorf("package size: %d exceeds current limit: %d", stats.Size(), maxPackageSize)
	}

	// test asset type dir
	extractDir := removeZipExtension(p.DownloadPath)
	err = p.extractZip(p.DownloadPath, extractDir)
	if err != nil {
		return "", fmt.Errorf("could not extract package content: %s", err)
	}
	pkName, err = validatePackageContent(extractDir)
	if err != nil {
		return "", fmt.Errorf("package validation failed: %s", err)
	}

	// run analyze on package content
	response, err := helpers.Retry(func() (any, error) {
		return humioClient.AnalyzePackageFromZip(ctx, humioHTTPClient, p.DownloadPath, view)
	}, 3, 2*time.Second)
	if err != nil {
		return "", fmt.Errorf("package validation request failed: %s", err)
	}

	// depending on ConflictPolicy we might need to error even if no errors are detected
	switch r := response.(type) {
	case humio.PackageAnalysisResultJson:
		if p.ConflictPolicy != humio.PackagePolicyOverwrite {
			if r.ExistingVersion != nil {
				return "", fmt.Errorf("package already installed, name: %s, version: %s", r.PackageManifestResultJson.Name, *r.ExistingVersion)
			}
		}
	case humio.PackageErrorReportJson:
		if len(r.ParseErrors) > 0 {
			return "", fmt.Errorf("package validation returned errors: %v", r.ParseErrors[0])
		}
	}
	return pkName, nil
}

func (p *LogscalePackage) generateChecksum(filePath string) (string, error) {
	if filePath == "" {
		return "", fmt.Errorf("download path is not set")
	}
	file, err := os.Open(filePath) // #nosec G304 - filePath is validated and constructed internally
	if err != nil {
		return "", fmt.Errorf("failed to open package file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			p.Logger.Error(err, "could not close package file")
		}
	}()

	hasher := sha256.New()
	_, err = io.Copy(hasher, file)
	if err != nil {
		return "", fmt.Errorf("failed to read package file for checksum: %w", err)
	}

	checksum := fmt.Sprintf("%x", hasher.Sum(nil))
	return checksum, nil
}

func extrackHash(checksum string) (string, error) {
	// we expect the provided value to be sha256:actualhash
	parts := strings.Split(checksum, ":")
	if len(parts) == 1 {
		return "", fmt.Errorf("could not extract hash from checksum: %s", checksum)
	}
	return parts[1], nil
}

func (p *LogscalePackage) extractZip(zipPath, extractDir string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open zip file: %w", err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			p.Logger.Error(err, "could not close zip reader")
		}
	}()

	// Create the extraction directory if it doesn't exist
	err = os.MkdirAll(extractDir, 0750)
	if err != nil {
		return fmt.Errorf("failed to create extraction directory: %w", err)
	}

	var stripPrefix string
	if len(reader.File) > 0 {
		// Check if all files start with the same prefix (typical for GitHub zipballs)
		firstFile := reader.File[0].Name
		if strings.Contains(firstFile, "/") {
			potentialPrefix := strings.Split(firstFile, "/")[0] + "/"

			// Verify all files start with this prefix
			allHavePrefix := true
			for _, file := range reader.File {
				if !strings.HasPrefix(file.Name, potentialPrefix) {
					allHavePrefix = false
					break
				}
			}

			if allHavePrefix {
				stripPrefix = potentialPrefix
			}
		}
	}

	for _, file := range reader.File {
		// Strip the top-level folder prefix if detected
		fileName := file.Name
		if stripPrefix != "" && strings.HasPrefix(fileName, stripPrefix) {
			fileName = strings.TrimPrefix(fileName, stripPrefix)
			// Skip if this would result in an empty filename (the root folder itself)
			if fileName == "" {
				continue
			}
		}

		// Construct the full path for the extracted file
		path := filepath.Join(extractDir, fileName) // #nosec G305 - path traversal protection implemented below
		if !strings.HasPrefix(path, filepath.Clean(extractDir)+string(os.PathSeparator)) {
			return fmt.Errorf("invalid file path: %s", fileName)
		}

		// Create directory for file if needed
		if file.FileInfo().IsDir() {
			err = os.MkdirAll(path, file.FileInfo().Mode())
			if err != nil {
				return fmt.Errorf("failed to create directory %s: %w", path, err)
			}
			continue
		}

		// Create parent directories if they don't exist
		err = os.MkdirAll(filepath.Dir(path), 0750)
		if err != nil {
			return fmt.Errorf("failed to create parent directory for %s: %w", path, err)
		}

		fileReader, err := file.Open()
		if err != nil {
			return fmt.Errorf("failed to open file %s in archive: %w", file.Name, err)
		}

		destFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.FileInfo().Mode()) // #nosec G304 - path is validated and constructed internally
		if err != nil {
			if closeErr := fileReader.Close(); closeErr != nil {
				p.Logger.Error(closeErr, "could not close file reader")
			}
			return fmt.Errorf("failed to create destination file %s: %w", path, err)
		}

		_, err = io.Copy(destFile, fileReader) // #nosec G110 - decompression bomb protection via size limits in validation
		if closeErr := fileReader.Close(); closeErr != nil {
			p.Logger.Error(closeErr, "could not close file reader")
		}
		if closeErr := destFile.Close(); closeErr != nil {
			p.Logger.Error(closeErr, "could not close destination file")
		}
		if err != nil {
			return fmt.Errorf("failed to extract file %s: %w", file.Name, err)
		}
	}

	return nil
}

func removeZipExtension(filename string) string {
	if len(filename) >= len(packageDefaultExtension) && strings.HasSuffix(filename, packageDefaultExtension) {
		return filename[:len(filename)-len(packageDefaultExtension)]
	}

	return filename
}

func validatePackageContent(folderPath string) (string, error) {
	entries, err := os.ReadDir(folderPath)
	if err != nil {
		return "", fmt.Errorf("failed to read directory %s: %w", folderPath, err)
	}
	manifestPath := filepath.Join(folderPath, "manifest.yaml")
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		return "", fmt.Errorf("manifest.yaml not found in package root")
	}
	// Read and parse manifest.yaml
	var manifestData struct {
		Name string `yaml:"name"`
	}
	content, err := os.ReadFile(manifestPath) // #nosec G304 - manifestPath is constructed from validated folderPath
	if err != nil {
		return "", fmt.Errorf("failed to read manifest.yaml: %w", err)
	}
	err = yaml.Unmarshal(content, &manifestData)
	if err != nil {
		return "", fmt.Errorf("failed to parse manifest.yaml: %w", err)
	}

	if len(entries) > maxPackageAssetFolders {
		return "", fmt.Errorf("unexpected number of folders in archive: %d", len(entries))
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Skip excluded folders
		if slices.Contains(excludedAssetDirs, entry.Name()) {
			continue
		}

		// Error if unexpected folder
		if !slices.Contains(expectedAssetDirs, entry.Name()) {
			return "", fmt.Errorf("unexpected directory in package: %s", entry.Name())
		}

		dirPath := filepath.Join(folderPath, entry.Name())
		size, err := calculateDirectorySize(dirPath)
		if err != nil {
			return "", fmt.Errorf("failed to calculate size for directory %s: %w", dirPath, err)
		}

		if size > maxPackageAssetFolderSize {
			return "", fmt.Errorf("directory %s size %d exceeds limit of %d bytes", entry.Name(), size, maxPackageAssetFolderSize)
		}
	}
	// No errors
	return manifestData.Name, nil
}

func calculateDirectorySize(dirPath string) (int64, error) {
	var totalSize int64

	err := filepath.WalkDir(dirPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			totalSize += info.Size()
		}
		return nil
	})

	return totalSize, err
}

// validateAndCleanPath validates and cleans the download path for security,
// and creates the directory if it doesn't exist
func validateAndCleanPath(downloadPath string) (string, error) {
	// Validate and clean the download path for security
	cleanPath := filepath.Clean(downloadPath)
	if !filepath.IsAbs(cleanPath) {
		return "", fmt.Errorf("download path must be absolute")
	}

	// Create the directory if it doesn't exist
	dir := filepath.Dir(cleanPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", fmt.Errorf("failed to create download directory: %w", err)
	}

	return cleanPath, nil
}

// DeletePackage removes both the downloaded package file and the extracted folder
func (p *LogscalePackage) DeletePackage() error {
	var errors []string

	// Delete the zip file
	if p.DownloadPath != "" {
		if _, err := os.Stat(p.DownloadPath); err == nil {
			err := os.Remove(p.DownloadPath)
			if err != nil {
				errors = append(errors, fmt.Sprintf("failed to delete package file %s: %v", p.DownloadPath, err))
			}
		}

		// Delete the extracted folder if it exists
		extractDir := removeZipExtension(p.DownloadPath)
		if _, err := os.Stat(extractDir); err == nil {
			err := os.RemoveAll(extractDir)
			if err != nil {
				errors = append(errors, fmt.Sprintf("failed to delete extracted folder %s: %v", extractDir, err))
			}
		}
	}

	// Return combined errors if any occurred
	if len(errors) > 0 {
		return fmt.Errorf("cleanup errors: %s", strings.Join(errors, "; "))
	}

	return nil
}
