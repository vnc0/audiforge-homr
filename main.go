package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	uploadDir       = "/tmp/uploads"
	downloadDir     = "/tmp/downloads"
	cleanupInterval = time.Hour
	fileTTL         = time.Hour
)

var supportedExtensions = map[string]bool{
	".pdf":  true,
	".png":  true,
	".jpg":  true,
	".jpeg": true,
}

type ProcessingStatus struct {
	Status       string `json:"status"`
	Message      string `json:"message"`
	Timestamp    int64  `json:"timestamp"`
	PageCount    int    `json:"pageCount,omitempty"`
	DownloadName string `json:"downloadName,omitempty"`
}

var (
	processing sync.Map
	templates  *template.Template
)

func init() {
	must(os.MkdirAll(uploadDir, 0o755))
	must(os.MkdirAll(downloadDir, 0o755))
	templates = template.Must(template.ParseGlob("/app/templates/*.html"))
	go startCleanupRoutine()
}

func main() {
	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/upload", uploadHandler)
	http.HandleFunc("/status/", statusHandler)
	http.HandleFunc("/download/", downloadHandler)
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("/app/templates"))))

	log.Println("Server started on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func startCleanupRoutine() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		cleanupJobs(uploadDir)
		cleanupJobs(downloadDir)
	}
}

func cleanupJobs(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		log.Printf("cleanup: unable to read %s: %v", root, err)
		return
	}

	for _, entry := range entries {
		target := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil {
			log.Printf("cleanup: unable to stat %s: %v", target, err)
			continue
		}
		if time.Since(info.ModTime()) <= fileTTL {
			continue
		}
		if err := os.RemoveAll(target); err != nil {
			log.Printf("cleanup: unable to remove %s: %v", target, err)
			continue
		}
		log.Printf("cleanup: removed %s", target)
	}
}

func indexHandler(w http.ResponseWriter, _ *http.Request) {
	if err := templates.ExecuteTemplate(w, "index.html", nil); err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Invalid file upload", http.StatusBadRequest)
		return
	}
	defer file.Close()

	extension := strings.ToLower(filepath.Ext(header.Filename))
	if !supportedExtensions[extension] {
		http.Error(w, "Only PDF, PNG, JPG, and JPEG files are supported", http.StatusBadRequest)
		return
	}

	id := uuid.NewString()
	uploadPath, err := saveUploadedFile(id, extension, file)
	if err != nil {
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}

	downloadName := downloadFilename(header.Filename)
	storeStatus(id, ProcessingStatus{
		Status:       "processing",
		Message:      "Upload received, preparing conversion",
		Timestamp:    time.Now().Unix(),
		DownloadName: downloadName,
	})

	go processFile(id, uploadPath, downloadName)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
}

func saveUploadedFile(id, extension string, source io.Reader) (string, error) {
	jobDir := filepath.Join(uploadDir, id)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		return "", err
	}

	filename := "source" + extension
	targetPath := filepath.Join(jobDir, filename)
	targetFile, err := os.Create(targetPath)
	if err != nil {
		return "", err
	}
	defer targetFile.Close()

	if _, err := io.Copy(targetFile, source); err != nil {
		return "", err
	}

	return targetPath, nil
}

func processFile(id, inputPath, downloadName string) {
	outputDir := filepath.Join(downloadDir, id)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		storeError(id, fmt.Errorf("failed to create output directory: %w", err))
		return
	}

	logFile, err := os.OpenFile(filepath.Join(outputDir, "conversion.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		storeError(id, fmt.Errorf("failed to create log file: %w", err))
		return
	}
	defer logFile.Close()

	logger := io.Writer(logFile)
	if isDebugEnabled() {
		logger = io.MultiWriter(logFile, os.Stdout)
	}

	storeStatus(id, ProcessingStatus{
		Status:       "processing",
		Message:      "Preparing input pages",
		Timestamp:    time.Now().Unix(),
		DownloadName: downloadName,
	})

	imagePaths, err := prepareInputImages(inputPath, logger)
	if err != nil {
		storeError(id, err)
		return
	}

	pageOutputs := make([]string, 0, len(imagePaths))
	for index, imagePath := range imagePaths {
		storeStatus(id, ProcessingStatus{
			Status:       "processing",
			Message:      fmt.Sprintf("Running homr on page %d of %d", index+1, len(imagePaths)),
			Timestamp:    time.Now().Unix(),
			PageCount:    len(imagePaths),
			DownloadName: downloadName,
		})

		if err := runCommand(logger, "homr", imagePath); err != nil {
			storeError(id, fmt.Errorf("homr failed on page %d: %w", index+1, err))
			return
		}

		xmlPath := replaceExtension(imagePath, ".musicxml")
		if _, err := os.Stat(xmlPath); err != nil {
			storeError(id, fmt.Errorf("homr did not produce MusicXML for page %d", index+1))
			return
		}

		target := filepath.Join(outputDir, fmt.Sprintf("page-%03d.musicxml", index+1))
		if err := moveFile(xmlPath, target); err != nil {
			storeError(id, fmt.Errorf("failed to collect page %d output: %w", index+1, err))
			return
		}

		pageOutputs = append(pageOutputs, target)
	}

	finalOutput := filepath.Join(outputDir, "result.musicxml")
	switch len(pageOutputs) {
	case 0:
		storeError(id, fmt.Errorf("no MusicXML output was generated"))
		return
	case 1:
		if err := copyFile(pageOutputs[0], finalOutput); err != nil {
			storeError(id, fmt.Errorf("failed to prepare final MusicXML: %w", err))
			return
		}
	default:
		storeStatus(id, ProcessingStatus{
			Status:       "processing",
			Message:      "Merging page MusicXML files",
			Timestamp:    time.Now().Unix(),
			PageCount:    len(pageOutputs),
			DownloadName: downloadName,
		})

		args := append(append([]string{}, pageOutputs...), "-o", finalOutput)
		if err := runCommand(logger, "relieur", args...); err != nil {
			storeError(id, fmt.Errorf("relieur failed while merging pages: %w", err))
			return
		}
	}

	if _, err := os.Stat(finalOutput); err != nil {
		storeError(id, fmt.Errorf("final MusicXML file was not created"))
		return
	}

	storeStatus(id, ProcessingStatus{
		Status:       "completed",
		Message:      fmt.Sprintf("Converted %d page(s) to MusicXML", len(pageOutputs)),
		Timestamp:    time.Now().Unix(),
		PageCount:    len(pageOutputs),
		DownloadName: downloadName,
	})
}

func prepareInputImages(inputPath string, logger io.Writer) ([]string, error) {
	extension := strings.ToLower(filepath.Ext(inputPath))
	switch extension {
	case ".pdf":
		workDir := filepath.Dir(inputPath)
		if err := runCommand(logger, "pdftoppm", "-r", "300", "-png", inputPath, filepath.Join(workDir, "page")); err != nil {
			return nil, fmt.Errorf("failed to rasterize PDF: %w", err)
		}

		imagePaths, err := filepath.Glob(filepath.Join(workDir, "page-*.png"))
		if err != nil {
			return nil, err
		}
		sortImagePaths(imagePaths)
		if len(imagePaths) == 0 {
			return nil, fmt.Errorf("PDF rasterization produced no pages")
		}
		return imagePaths, nil
	case ".png", ".jpg", ".jpeg":
		return []string{inputPath}, nil
	default:
		return nil, fmt.Errorf("unsupported input type: %s", extension)
	}
}

func sortImagePaths(paths []string) {
	sort.Slice(paths, func(i, j int) bool {
		return pageNumber(paths[i]) < pageNumber(paths[j])
	})
}

func pageNumber(path string) int {
	base := filepath.Base(path)
	hyphen := strings.LastIndex(base, "-")
	dot := strings.LastIndex(base, ".")
	if hyphen == -1 || dot == -1 || hyphen >= dot {
		return 0
	}
	number, err := strconv.Atoi(base[hyphen+1 : dot])
	if err != nil {
		return 0
	}
	return number
}

func runCommand(logWriter io.Writer, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = logWriter
	cmd.Stderr = logWriter
	return cmd.Run()
}

func replaceExtension(path, extension string) string {
	return strings.TrimSuffix(path, filepath.Ext(path)) + extension
}

func moveFile(source, target string) error {
	if err := os.Rename(source, target); err == nil {
		return nil
	}
	if err := copyFile(source, target); err != nil {
		return err
	}
	return os.Remove(source)
}

func copyFile(source, target string) error {
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(target)
	if err != nil {
		return err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	return dst.Close()
}

func storeError(id string, err error) {
	storeStatus(id, ProcessingStatus{
		Status:    "error",
		Message:   err.Error(),
		Timestamp: time.Now().Unix(),
	})
}

func storeStatus(id string, status ProcessingStatus) {
	processing.Store(id, status)
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/status/")
	status, ok := processing.Load(id)
	if !ok {
		http.Error(w, "Invalid ID", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func downloadHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/download/")
	statusValue, ok := processing.Load(id)
	if !ok {
		http.Error(w, "Invalid ID", http.StatusNotFound)
		return
	}

	status := statusValue.(ProcessingStatus)
	if status.Status != "completed" {
		http.Error(w, "Conversion not completed yet", http.StatusConflict)
		return
	}

	outputPath := filepath.Join(downloadDir, id, "result.musicxml")
	if _, err := os.Stat(outputPath); err != nil {
		http.Error(w, "MusicXML file not found", http.StatusNotFound)
		return
	}

	downloadName := status.DownloadName
	if downloadName == "" {
		downloadName = id + ".musicxml"
	}

	w.Header().Set("Content-Type", "application/vnd.recordare.musicxml+xml")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", downloadName))
	http.ServeFile(w, r, outputPath)
}

func downloadFilename(inputName string) string {
	base := strings.TrimSuffix(filepath.Base(inputName), filepath.Ext(inputName))
	base = strings.TrimSpace(base)
	if base == "" {
		base = "score"
	}

	replacer := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		"\"", "",
		"'", "",
	)
	base = replacer.Replace(base)
	return base + ".musicxml"
}

func isDebugEnabled() bool {
	return strings.EqualFold(os.Getenv("LOG"), "debug")
}
