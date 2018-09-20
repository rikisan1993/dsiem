package xcorrelator

import (
	"dsiem/internal/shared/pkg/fs"
	log "dsiem/internal/shared/pkg/logger"
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/elastic/apm-agent-go"
)

const (
	intelFileGlob           = "intel_*.json"
	maxSecondToWaitForIntel = 2
)

// IntelEnabled mark whether intel lookup is enabled
var IntelEnabled bool

type intelSource struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Enabled     bool     `json:"enabled"`
	URL         string   `json:"url"`
	Matcher     string   `json:"matcher"`
	ResultRegex []string `json:"result_regex"`
}

// IntelResult contain results from threat intel queries
type IntelResult struct {
	Provider string `json:"provider"`
	Term     string `json:"term"`
	Result   string `json:"result"`
}

type intelSources struct {
	IntelSources []intelSource `json:"intel_sources"`
}

var intels intelSources

// CheckIntelIP lookup ip on threat intel references
func CheckIntelIP(ip string, connID uint64) (found bool, results []IntelResult) {
	defer func() {
		if r := recover(); r != nil {
			log.Warn("Panic occurred while checking intel for "+ip, connID)
		}
	}()

	term := ip

	for _, v := range intels.IntelSources {
		url := strings.Replace(v.URL, "${ip}", ip, 1)
		c := http.Client{Timeout: time.Second * maxSecondToWaitForIntel}
		req, err := http.NewRequest(http.MethodGet, url, nil)

		tx := elasticapm.DefaultTracer.StartTransaction("Threat Intel Lookup", "SIEM")
		tx.Context.SetCustom("term", term)
		tx.Context.SetCustom("provider", v.Name)
		tx.Context.SetCustom("Url", url)

		if err != nil {
			log.Warn("Cannot create new HTTP request for "+v.Name+" TI.", connID)
			tx.Result = "Cannot create HTTP request"
			tx.End()
			continue
		}
		res, err := c.Do(req)
		if err != nil {
			log.Warn("Failed to query "+v.Name+" TI for IP "+ip, connID)
			tx.Result = "Failed to query " + v.Name
			tx.End()
			continue
		}
		body, readErr := ioutil.ReadAll(res.Body)
		if readErr != nil {
			log.Warn("Cannot read result from "+v.Name+" TI for IP "+ip, connID)
			tx.Result = "Cannot read result from " + v.Name
			tx.End()
			continue
		}

		if v.Matcher == "regex" {
			f, r := matcherRegexIntel(body, v.Name, term, v.ResultRegex, connID)
			if f {
				found = true
				results = append(results, r...)
			}
		}
		if found {
			tx.Result = "Intel found"
		} else {
			tx.Result = "Intel not found"
		}
		tx.End()
	}
	return
}

// InitIntel initialize threat intel cross-correlation
func InitIntel(confDir string) error {
	p := path.Join(confDir, intelFileGlob)
	files, err := filepath.Glob(p)
	if err != nil {
		return err
	}

	for i := range files {
		var it intelSources
		if !fs.FileExist(files[i]) {
			return errors.New("Cannot find " + files[i])
		}
		file, err := os.Open(files[i])
		if err != nil {
			return err
		}
		defer file.Close()

		byteValue, _ := ioutil.ReadAll(file)
		err = json.Unmarshal(byteValue, &it)
		if err != nil {
			return err
		}
		for j := range it.IntelSources {
			if it.IntelSources[j].Enabled {
				intels.IntelSources = append(intels.IntelSources, it.IntelSources[j])
			}
		}
	}

	total := len(intels.IntelSources)
	if total > 0 {
		IntelEnabled = true
	}
	log.Info("Loaded "+strconv.Itoa(total)+" threat intelligence sources.", 0)

	return nil
}
