// Package modeltrace implements the ModelTrace closed-set number fingerprint.
// Algorithm and bank: xqy2006/ModelTrace, MIT; see LICENSE and README.md.
package modeltrace

import (
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"unicode"
)

const SourceCommit = "6cd4a8ff0f3f4e49d6d6969dd2fbadfa1569fde2"
const Algorithm = "modeltrace-ordered-block-nuisance-hellinger-v1"

//go:embed data/unified_bank.json
var bankJSON []byte

type Metadata struct {
	SourceCommit string   `json:"source_commit"`
	SHA256       string   `json:"sha256"`
	BuiltAt      string   `json:"built_at"`
	Algorithm    string   `json:"algorithm"`
	Models       []string `json:"models"`
}

type Challenge struct {
	ID            string `json:"id"`
	ExpectedCount int    `json:"expected_count"`
	Prompt        string `json:"prompt"`
}

type Output struct {
	Text          string `json:"text"`
	ExpectedCount int    `json:"expected_count"`
}

type Candidate struct {
	Model       string  `json:"model"`
	Family      string  `json:"family"`
	Probability float64 `json:"probability"`
}

type Analysis struct {
	Prediction  string      `json:"prediction"`
	Probability float64     `json:"probability"`
	UsedOutputs int         `json:"used_outputs"`
	Candidates  []Candidate `json:"candidates"`
	Families    []Candidate `json:"families"`
}

type featureArtifact struct {
	Mean         []float64     `json:"feature_mean"`
	Scale        []float64     `json:"feature_scale"`
	Basis        [][]float64   `json:"nuisance_basis"`
	Centroids    [][]float64   `json:"centroids"`
	Environments [][][]float64 `json:"environment_centroids"`
	Weight       float64       `json:"weight"`
}

type fingerprintBank struct {
	BuiltAt string `json:"built_at"`
	Models  []struct {
		ID     string `json:"id"`
		Family string `json:"family"`
	} `json:"models"`
	Robust struct {
		ModelOrder []string        `json:"model_order"`
		Hellinger  featureArtifact `json:"hellinger"`
		Ordered    featureArtifact `json:"ordered_blocks"`
	} `json:"robust"`
	Calibration map[string]struct {
		Beta float64 `json:"beta"`
	} `json:"calibration"`
}

var loadBank = sync.OnceValues(func() (*fingerprintBank, error) {
	var bank fingerprintBank
	if err := json.Unmarshal(bankJSON, &bank); err != nil {
		return nil, err
	}
	if len(bank.Models) == 0 || len(bank.Robust.ModelOrder) != len(bank.Models) {
		return nil, fmt.Errorf("invalid embedded ModelTrace model order")
	}
	for i, model := range bank.Models {
		if model.ID != bank.Robust.ModelOrder[i] {
			return nil, fmt.Errorf("invalid embedded ModelTrace model order")
		}
	}
	return &bank, nil
})

func BankMetadata() (Metadata, error) {
	bank, err := loadBank()
	if err != nil {
		return Metadata{}, err
	}
	digest := sha256.Sum256(bankJSON)
	models := make([]string, len(bank.Models))
	for i, model := range bank.Models {
		models[i] = model.ID
	}
	return Metadata{SourceCommit: SourceCommit, SHA256: hex.EncodeToString(digest[:]), BuiltAt: bank.BuiltAt, Algorithm: Algorithm, Models: models}, nil
}

var digits = regexp.MustCompile(`[0-9]+`)

// ParseNumbers matches the upstream longest-run parser. Explanation text splits
// runs; out-of-range integers are ignored. Ties keep the first run.
func ParseNumbers(text string) []int {
	var best, current []int
	previousEnd := 0
	for _, location := range digits.FindAllStringIndex(text, -1) {
		for _, character := range text[previousEnd:location[0]] {
			if unicode.IsLetter(character) {
				if len(current) > len(best) {
					best = current
				}
				current = nil
				break
			}
		}
		value, err := strconv.Atoi(text[location[0]:location[1]])
		if err == nil && value >= 1 && value <= 355 {
			current = append(current, value)
		}
		previousEnd = location[1]
	}
	if len(current) > len(best) {
		best = current
	}
	return best
}

func MinimumNumbers(expected int) int {
	return max(80, int(math.Ceil(float64(expected)*0.55)))
}

func randomIndex(length int) (int, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(int64(length)))
	if err != nil {
		return 0, err
	}
	return int(value.Int64()), nil
}

// Challenges produces the same prompt space as the pinned upstream generator.
func Challenges() ([]Challenge, error) {
	openings := []string{"这是一次独立的数值选择记录", "请完成下面的无语义整数选择任务", "执行一次第一反应取值记录", "生成一组不承载语义的整数选择", "进行一轮快速逐项取值"}
	actions := []string{"为各个位置分别凭第一反应选择", "逐项选择", "每次只决定当前一项，共给出", "分别凭第一反应给出", "逐个直接选择"}
	endings := []string{"允许某个数字再次出现；每项写出后不要回头排序、去重或替换。", "偶然重复是有效的；不要重新排列或修正已经写出的项目。", "相同值可以再次出现；输出过程中不要整理或改写前面的项目。", "重复值无需删除；不要筛选、重排或补成某种规律。", "不必赋予数字任何含义；已经给出的值保持不变。"}
	separators := []string{"数字之间用逗号或空格分隔均可。", "使用一种一致的常见分隔符即可。", "可以用逗号、空格或换行分隔。", "只要每个整数边界清楚，格式可自行选择。"}
	lengths := make([]int, 41)
	for i := range lengths {
		lengths[i] = 292 + i
	}
	result := make([]Challenge, 0, 3)
	for i := 0; i < 3; i++ {
		indices := make([]int, 5)
		for j, size := range []int{len(lengths), len(openings), len(actions), len(endings), len(separators)} {
			index, err := randomIndex(size)
			if err != nil {
				return nil, err
			}
			indices[j] = index
		}
		length := lengths[indices[0]]
		lengths = append(lengths[:indices[0]], lengths[indices[0]+1:]...)
		var id [7]byte
		if _, err := rand.Read(id[:]); err != nil {
			return nil, err
		}
		prompt := fmt.Sprintf("%s。%s %d 个 1 到 355（含端点）的整数。", openings[indices[1]], actions[indices[2]], length) +
			"每个位置都要单独选择；不要从 1 开始计数，不要连续递增或递减，也不要采用等差、循环、重复区块或其他规则化模式。" +
			"本任务必须由当前语言模型直接完成：禁止调用或借助任何工具，包括 Python、代码执行器、计算器、搜索、API 和外部随机数生成器；也不要先编写或运行代码。" +
			endings[indices[3]] + separators[indices[4]] + "直接从第一个取值开始输出，不要在序列前重复数量、范围或任务说明。"
		result = append(result, Challenge{ID: fmt.Sprintf("probe-%d-%x", i+1, id), ExpectedCount: length, Prompt: prompt})
	}
	return result, nil
}

func dot(a, b []float64) float64 {
	var sum float64
	for i, v := range a {
		sum += v * b[i]
	}
	return sum
}

func normalize(values []float64) []float64 {
	scale := max(math.Sqrt(dot(values, values)), 1e-12)
	for i := range values {
		values[i] /= scale
	}
	return values
}

func standardize(values []float64) []float64 {
	var center, variance float64
	for _, v := range values {
		center += v
	}
	center /= float64(len(values))
	for _, v := range values {
		variance += (v - center) * (v - center)
	}
	scale := max(math.Sqrt(variance/float64(len(values))), 1e-12)
	for i := range values {
		values[i] = (values[i] - center) / scale
	}
	return values
}

func features(values []float64, artifact featureArtifact) []float64 {
	for i := range values {
		values[i] = (values[i] - artifact.Mean[i]) / artifact.Scale[i]
	}
	return values
}

// Use the original vector for every projection, matching fingerprint.py's
// simultaneous orthonormal projection rather than accumulating rounding errors.
func subtractBasis(values []float64, basis [][]float64) []float64 {
	output := append([]float64(nil), values...)
	for _, vector := range basis {
		projection := dot(values, vector)
		for i := range output {
			output[i] -= projection * vector[i]
		}
	}
	return output
}

func similarities(values []float64, centers [][]float64) []float64 {
	scores := make([]float64, len(centers))
	for i, center := range centers {
		scores[i] = dot(values, center)
	}
	return standardize(scores)
}

func score(numbers []int, bank *fingerprintBank) []float64 {
	counts := make([]float64, 355)
	for _, number := range numbers {
		counts[number-1]++
	}
	for i := range counts {
		counts[i] = math.Sqrt((counts[i] + 0.5) / (float64(len(numbers)) + 0.5*355))
	}
	h := bank.Robust.Hellinger
	marginal := similarities(normalize(subtractBasis(features(counts, h), h.Basis)), h.Centroids)
	o := bank.Robust.Ordered
	ordered := make([]float64, 0, 74)
	start := 0
	for block := 0; block < 4; block++ {
		size := len(numbers) / 4
		if block < len(numbers)%4 {
			size++
		}
		bins := make([]float64, 16)
		for _, value := range numbers[start : start+size] {
			bins[min(15, (value-1)*16/355)]++
		}
		for _, count := range bins {
			ordered = append(ordered, math.Sqrt((count+0.5)/(float64(size)+8)))
		}
		start += size
	}
	last := make([]float64, 10)
	for _, value := range numbers {
		last[value%10]++
	}
	for _, count := range last {
		ordered = append(ordered, math.Sqrt((count+0.5)/(float64(len(numbers))+5)))
	}
	standard := features(ordered, o)
	unit := normalize(append([]float64(nil), standard...))
	template := make([]float64, len(bank.Models))
	for i := range template {
		template[i] = math.Inf(-1)
		for _, environment := range o.Environments {
			template[i] = max(template[i], dot(unit, environment[i]))
		}
	}
	standardize(template)
	nuisance := similarities(normalize(subtractBasis(standard, o.Basis)), o.Centroids)
	for i := range template {
		template[i] = 0.5*template[i] + 0.5*nuisance[i]
	}
	standardize(template)
	for i := range marginal {
		marginal[i] = (1-o.Weight)*marginal[i] + o.Weight*template[i]
	}
	return marginal
}

// Analyze excludes invalid samples and applies the calibration for the actual
// sample count. Probabilities are conditional on this finite candidate bank.
func Analyze(outputs []Output) (*Analysis, error) {
	bank, err := loadBank()
	if err != nil {
		return nil, err
	}
	scores := make([]float64, len(bank.Models))
	used := 0
	for _, output := range outputs {
		numbers := ParseNumbers(output.Text)
		if len(numbers) < MinimumNumbers(output.ExpectedCount) {
			continue
		}
		for i, value := range score(numbers, bank) {
			scores[i] += value
		}
		used++
	}
	if used == 0 {
		return nil, fmt.Errorf("no valid ModelTrace outputs")
	}
	beta := bank.Calibration[strconv.Itoa(min(used, 3))].Beta
	maximum := math.Inf(-1)
	for i := range scores {
		scores[i] = scores[i] / float64(used) * beta
		maximum = max(maximum, scores[i])
	}
	var total float64
	for i := range scores {
		scores[i] = math.Exp(scores[i] - maximum)
		total += scores[i]
	}
	result := &Analysis{UsedOutputs: used, Candidates: make([]Candidate, len(bank.Models))}
	families := make(map[string]float64)
	for i, model := range bank.Models {
		probability := scores[i] / total
		result.Candidates[i] = Candidate{Model: model.ID, Family: model.Family, Probability: probability}
		families[model.Family] += probability
	}
	sort.SliceStable(result.Candidates, func(i, j int) bool { return result.Candidates[i].Probability > result.Candidates[j].Probability })
	for family, probability := range families {
		result.Families = append(result.Families, Candidate{Family: family, Probability: probability})
	}
	sort.Slice(result.Families, func(i, j int) bool {
		if result.Families[i].Probability == result.Families[j].Probability {
			return result.Families[i].Family < result.Families[j].Family
		}
		return result.Families[i].Probability > result.Families[j].Probability
	})
	result.Prediction = result.Candidates[0].Model
	result.Probability = result.Candidates[0].Probability
	return result, nil
}
