package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type HFModel struct {
	Name                         string   `json:"name"`
	Provider                     string   `json:"provider"`
	ParameterCount               string   `json:"parameter_count"`
	ParametersRaw                int64    `json:"parameters_raw"`
	MinRamGB                     float64  `json:"min_ram_gb"`
	RecommendedRAMGB             float64  `json:"recommended_ram_gb"`
	MinVRAMGB                    float64  `json:"min_vram_gb"`
	Quantization                 string   `json:"quantization"`
	Format                       string   `json:"format"`
	ContextLength                int      `json:"context_length"`
	UseCase                      string   `json:"use_case"`
	Capabilities                 []string `json:"capabilities"`
	PipelineTag                  string   `json:"pipeline_tag"`
	Architecture                 string   `json:"architecture"`
	HFDownloads                  int64    `json:"hf_downloads"`
	HFLikes                      int64    `json:"hf_likes"`
	ReleaseDate                  *string  `json:"release_date"`
	NumHiddenLayers              *int     `json:"num_hidden_layers"`
	NumAttentionHeads            *int     `json:"num_attention_heads"`
	NumKeyValueHeads             *int     `json:"num_key_value_heads"`
	HeadDim                      *int     `json:"head_dim"`
	HiddenSize                   *int     `json:"hidden_size"`
	VocabSize                    *int     `json:"vocab_size"`
	MoeIntermediateSize          *int     `json:"moe_intermediate_size"`
	SharedExpertIntermediateSize *int     `json:"shared_expert_intermediate_size"`
	IsMoe                        bool     `json:"is_moe"`
	NumExperts                   *int     `json:"num_experts"`
	ActiveExperts                *int     `json:"active_experts"`
	ActiveParameters             *int64   `json:"active_parameters"`
	Discovered                   bool     `json:"_discovered"`
}

var trustedProviders = map[string]bool{
	"qwen":                  true,
	"alibaba":               true,
	"meta-llama":            true,
	"meta":                  true,
	"deepseek-ai":           true,
	"deepseek":              true,
	"google":                true,
	"microsoft":             true,
	"mistral":               true,
	"mistralai":             true,
	"anthropic":             true,
	"nvidia":                true,
	"amd":                   true,
	"intel":                 true,
	"unsloth":               true,
	"mlx-community":         true,
	"lmstudio-community":    true,
	"ggml-org":              true,
	"mradermacher":          true,
	"baai":                  true,
	"thudm":                 true,
	"01-ai":                 true,
	"yi":                    true,
	"databricks":            true,
	"nousresearch":          true,
	"bartowski":             true,
	"thebloke":              true,
	"thedrummer":            true,
	"maziyarpanahi":         true,
	"apple":                 true,
	"cohere":                true,
	"openai":                true,
	"huggingface":           true,
	"stabilityai":           true,
	"stability-ai":          true,
	"salesforce":            true,
	"allenai":               true,
	"bigcode":               true,
	"tiiuae":                true,
	"liquidai":              true,
	"deepmind":              true,
	"amazon":                true,
	"redhatai":              true,
	"h2oai":                 true,
	"upstage":               true,
	"mosaicml":              true,
	"neuralmagic":           true,
	"lm-sys":                true,
	"lmsys":                 true,
	"bigscience":            true,
	"bigscience-workshop":   true,
	"snowflake":             true,
	"cerebras":              true,
	"sambanova":             true,
	"graphcore":             true,
	"habana":                true,
	"lightning-ai":          true,
	"writer":                true,
	"replicate":             true,
	"together":              true,
	"anyscale":              true,
	"fireworks":             true,
	"perplexity":            true,
	"groq":                  true,
	"ibm":                   true,
	"qualcomm":              true,
	"arm":                   true,
	"zhipu":                 true,
	"minimax":               true,
	"moonshotai":            true,
	"kimi":                  true,
	"internlm":              true,
	"openbmb":               true,
	"hfl":                   true,
	"chatglm":               true,
	"flag":                  true,
	"tencent":               true,
	"baidu":                 true,
	"bytedance":             true,
	"huawei":                true,
	"stepfun":               true,
	"phind":                 true,
	"teknium":               true,
	"mlabonne":              true,
	"open-orca":             true,
	"llm-jp":                true,
	"rinna":                 true,
	"line":                  true,
	"stockmark":             true,
	"cyberagent":            true,
	"tokyotech":             true,
	"cognitivecomputations": true,
	"ehartford":             true,
	"bond":                  true,
	"prometh-ai":            true,
	"openchat":              true,
	"garage-baid":           true,
	"m-a-p":                 true,
	"fblgit":                true,
	"nlp-mt":                true,
	"fixie-ai":              true,
	"lightblue":             true,
	"snunlp":                true,
	"kakao":                 true,
	"naver":                 true,
	"clova":                 true,
	"pfnet":                 true,
	"abeja":                 true,
	"sakana-ai":             true,
	"pcl":                   true,
	"pengcheng":             true,
	"sensenova":             true,
	"megvii":                true,
	"tmnet":                 true,
}

var paramRegex = regexp.MustCompile(`(?i)(\d+\.?\d*)\s*[bB]`)

func extractNameParamCount(name string) float64 {
	matches := paramRegex.FindAllStringSubmatch(name, -1)
	if len(matches) == 0 {
		return 0
	}
	val, err := strconv.ParseFloat(matches[0][1], 64)
	if err != nil {
		return 0
	}
	return val
}

func nullInt(v *int) string {
	if v == nil {
		return "NULL"
	}
	return strconv.Itoa(*v)
}

func nullInt64(v *int64) string {
	if v == nil {
		return "NULL"
	}
	return strconv.FormatInt(*v, 10)
}
func escapeSQL(s string) string {
	s = strings.ReplaceAll(s, "'", "''")
	return s
}

func nullString(v *string) string {
	if v == nil {
		return "NULL"
	}
	return "'" + escapeSQL(*v) + "'"
}

func main() {
	data, err := os.ReadFile("hf_models.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading hf_models.json: %v\n", err)
		os.Exit(1)
	}

	var models []HFModel
	if err := json.Unmarshal(data, &models); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing JSON: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Read %d models from hf_models.json\n", len(models))

	var sb strings.Builder
	sb.WriteString("DELETE FROM hf_models;\n")
	sb.WriteString("INSERT INTO hf_models (name, provider, parameter_count, parameters_raw, min_ram_gb, recommended_ram_gb, min_vram_gb, quantization, format, context_length, use_case, capabilities, pipeline_tag, architecture, hf_downloads, hf_likes, release_date, num_hidden_layers, num_attention_heads, num_key_value_heads, head_dim, hidden_size, vocab_size, moe_intermediate_size, shared_expert_intermediate_size, is_moe, num_experts, active_experts, active_parameters, trust_level) VALUES\n")

	trustedCount := 0
	untrustedCount := 0
	droppedCount := 0

	for i, m := range models {
		nameParamCount := extractNameParamCount(m.Name)
		provider := strings.ToLower(m.Provider)

		if nameParamCount >= 3 {
			expectedParams := nameParamCount * 1e9
			if m.ParametersRaw < int64(expectedParams*0.15) {
				droppedCount++
				continue
			}
		}

		trustLevel := "untrusted"
		if trustedProviders[provider] {
			trustLevel = "trusted"
		} else if nameParamCount > 0 {
			expectedParams := nameParamCount * 1e9
			if m.ParametersRaw >= int64(expectedParams*0.5) && m.MinVRAMGB >= 0.5 {
				trustLevel = "trusted"
			}
		}

		if trustLevel == "trusted" {
			trustedCount++
		} else {
			untrustedCount++
		}

		capsJSON := "[]"
		if len(m.Capabilities) > 0 {
			quoted := make([]string, len(m.Capabilities))
			for j, c := range m.Capabilities {
				quoted[j] = `"` + escapeSQL(c) + `"`
			}
			capsJSON = "[" + strings.Join(quoted, ",") + "]"
		}

		isMoe := 0
		if m.IsMoe {
			isMoe = 1
		}

		comma := ","
		if i == len(models)-1 {
			comma = ";"
		}

		sb.WriteString(fmt.Sprintf(
			"('%s', '%s', '%s', %d, %.2f, %.2f, %.2f, '%s', '%s', %d, '%s', '%s', '%s', '%s', %d, %d, %s, %s, %s, %s, %s, %s, %s, %s, %s, %d, %s, %s, %s, '%s')%s\n",
			escapeSQL(m.Name),
			escapeSQL(m.Provider),
			escapeSQL(m.ParameterCount),
			m.ParametersRaw,
			m.MinRamGB,
			m.RecommendedRAMGB,
			m.MinVRAMGB,
			escapeSQL(m.Quantization),
			escapeSQL(m.Format),
			m.ContextLength,
			escapeSQL(m.UseCase),
			capsJSON,
			escapeSQL(m.PipelineTag),
			escapeSQL(m.Architecture),
			m.HFDownloads,
			m.HFLikes,
			nullString(m.ReleaseDate),
			nullInt(m.NumHiddenLayers),
			nullInt(m.NumAttentionHeads),
			nullInt(m.NumKeyValueHeads),
			nullInt(m.HeadDim),
			nullInt(m.HiddenSize),
			nullInt(m.VocabSize),
			nullInt(m.MoeIntermediateSize),
			nullInt(m.SharedExpertIntermediateSize),
			isMoe,
			nullInt(m.NumExperts),
			nullInt(m.ActiveExperts),
			nullInt64(m.ActiveParameters),
			trustLevel,
			comma,
		))
	}

	os.MkdirAll("internal/db/migrations", 0755)
	if err := os.WriteFile("internal/db/migrations/004_seed_hf_models.sql", []byte(sb.String()), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing seed file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Generated internal/db/migrations/004_seed_hf_models.sql\n")
	fmt.Printf("  Trusted: %d\n", trustedCount)
	fmt.Printf("  Untrusted: %d\n", untrustedCount)
	fmt.Printf("  Dropped (unrealistic): %d\n", droppedCount)
}
