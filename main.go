package main

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

//go:embed ui.html
var uiHTML []byte

type resp struct {
	OK       bool     `json:"ok"`
	Error    string   `json:"error,omitempty"`
	FileName string   `json:"fileName,omitempty"`
	FileB64  string   `json:"fileB64,omitempty"`
	Text     string   `json:"text,omitempty"`
	Key      *Key     `json:"key,omitempty"`
	Stats    Stats    `json:"stats,omitempty"`
	Added    int      `json:"added,omitempty"`
	Unknown  []string `json:"unknown,omitempty"`
}

func parseKey(s string) (*Key, error) {
	k := NewKey()
	if strings.TrimSpace(s) == "" {
		return k, nil
	}
	if err := json.Unmarshal([]byte(s), k); err != nil {
		return nil, fmt.Errorf("түлхүүр файл буруу байна")
	}
	k.index()
	return k, nil
}

func outName(name, tag string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for _, t := range []string{"_НУУЦАЛСАН", "_СЭРГЭЭСЭН"} {
		base = strings.TrimSuffix(base, t)
	}
	return base + tag + ext
}

func processFile(mode, name string, data []byte, k *Key, st Stats) ([]byte, string, error) {
	ext := strings.ToLower(filepath.Ext(name))
	var out []byte
	var err error
	switch {
	case ext == ".xlsx" && mode == "anon":
		out, err = AnonymizeXlsx(data, k, st)
	case ext == ".xlsx":
		out, err = RestoreXlsx(data, k, st)
	case ext == ".docx" && mode == "anon":
		out, err = AnonymizeDocx(data, k, st)
	case ext == ".docx":
		out, err = RestoreDocx(data, k, st)
	case ext == ".txt" || ext == ".csv" || ext == ".md":
		if mode == "anon" {
			out = []byte(k.Anonymize(string(data), "", st))
		} else {
			out = []byte(k.Restore(string(data), st))
		}
	default:
		return nil, "", fmt.Errorf("зөвхөн .xlsx, .docx, .txt, .csv файл дэмжинэ (.xls, .doc бол Office-оор .xlsx/.docx болгож хадгална уу)")
	}
	if err != nil {
		return nil, "", err
	}
	tag := "_НУУЦАЛСАН"
	if mode != "anon" {
		tag = "_СЭРГЭЭСЭН"
	}
	return out, outName(name, tag), nil
}

func writeJSON(w http.ResponseWriter, r resp) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(r)
}

func handleProcess(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(200 << 20); err != nil {
		writeJSON(w, resp{Error: "хүсэлт уншиж чадсангүй"})
		return
	}
	mode := r.FormValue("mode")
	k, err := parseKey(r.FormValue("key"))
	if err != nil {
		writeJSON(w, resp{Error: err.Error()})
		return
	}
	st := Stats{}
	res := resp{OK: true, Stats: st}
	if f, h, err := r.FormFile("file"); err == nil {
		data, _ := io.ReadAll(f)
		f.Close()
		out, name, err := processFile(mode, h.Filename, data, k, st)
		if err != nil {
			writeJSON(w, resp{Error: err.Error()})
			return
		}
		res.FileName = name
		res.FileB64 = base64.StdEncoding.EncodeToString(out)
	} else {
		t := r.FormValue("text")
		if mode == "anon" {
			res.Text = k.Anonymize(t, "", st)
		} else {
			res.Text = k.Restore(t, st)
		}
	}
	res.Key = k
	res.Unknown = k.Unknown
	writeJSON(w, res)
}

func handleNames(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(200 << 20); err != nil {
		writeJSON(w, resp{Error: "хүсэлт уншиж чадсангүй"})
		return
	}
	k, err := parseKey(r.FormValue("key"))
	if err != nil {
		writeJSON(w, resp{Error: err.Error()})
		return
	}
	f, h, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, resp{Error: "файл сонгоогүй байна"})
		return
	}
	data, _ := io.ReadAll(f)
	f.Close()
	added := 0
	if strings.ToLower(filepath.Ext(h.Filename)) == ".xlsx" {
		added, err = CollectNamesFromXlsx(data, k)
		if err != nil {
			writeJSON(w, resp{Error: err.Error()})
			return
		}
	} else {
		for _, line := range strings.Split(string(data), "\n") {
			before := len(k.Names)
			k.AddName(strings.TrimSpace(strings.TrimPrefix(line, "\ufeff")))
			added += len(k.Names) - before
		}
	}
	writeJSON(w, resp{OK: true, Key: k, Added: added})
}

// CLI self-test: nuutslal -test file.xlsx
func selfTest(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	k := NewKey()
	st := Stats{}
	anon, name, err := processFile("anon", filepath.Base(path), data, k, st)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	dir := filepath.Dir(path)
	os.WriteFile(filepath.Join(dir, name), anon, 0644)
	kb, _ := json.MarshalIndent(k, "", " ")
	os.WriteFile(filepath.Join(dir, "key.json"), kb, 0644)
	fmt.Println("anon stats:", st)
	st2 := Stats{}
	k2, _ := parseKey(string(kb))
	back, name2, err := processFile("restore", name, anon, k2, st2)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	os.WriteFile(filepath.Join(dir, name2), back, 0644)
	fmt.Println("restore stats:", st2)
}

func main() {
	setupConsole()
	if len(os.Args) == 3 && os.Args[1] == "-test" {
		selfTest(os.Args[2])
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(uiHTML)
	})
	mux.HandleFunc("/api/process", handleProcess)
	mux.HandleFunc("/api/names", handleNames)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Println("Алдаа:", err)
		fmt.Scanln()
		return
	}
	url := "http://" + ln.Addr().String() + "/"
	fmt.Println("==============================================")
	fmt.Println("  НУУЦЛАГЧ – хувь хүний мэдээллийг нууцлах хэрэгсэл")
	fmt.Println("==============================================")
	fmt.Println("Хөтөч дээр нээгдлээ:", url)
	fmt.Println("Бүх ажил ЗӨВХӨН энэ компьютер дээр хийгдэнэ. Интернэт рүү юу ч илгээхгүй.")
	fmt.Println("Ажлаа дуусгаад энэ цонхыг хаавал программ унтарна.")
	openBrowser(url)
	http.Serve(ln, mux)
}
