// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
)

// WorkloadVersion identifies the generator. Results made with different
// versions are not comparable, and compare says so.
const WorkloadVersion = "w1"

// Item is one synthetic query or FAQ passage. Every name in it is invented.
type Item struct {
	Lang    string // "zh" or "en"
	Topic   int
	Product string
	Q       string // the question (or the query text)
	A       string // the short answer; empty for queries
}

// Workload is the whole deterministic synthetic corpus.
type Workload struct {
	Queries  []Item
	Passages []Item
}

var products = []string{"ZQ-200", "ZQ-310 Pro", "Nova K3", "Nova Air", "Lumo Mini", "Lumo Hub", "Orbit Z1", "Quill S5"}

type topic struct {
	zhQ [3]string
	enQ [3]string
	zhA string // %d is a number
	enA string
}

var topics = []topic{
	{[3]string{"%s怎么充电？", "%s充满电需要多长时间？", "我的%s充不进电怎么办？"},
		[3]string{"How do I charge the %s?", "How long does the %s take to fully charge?", "My %s will not charge, what should I do?"},
		"请使用原装充电线连接电源，约需%d小时充满，充电时指示灯为橙色。",
		"Use the original cable and adapter. A full charge takes about %d hours and the light stays orange."},
	{[3]string{"%s如何升级固件？", "%s的固件版本在哪里查看？", "%s固件升级失败了怎么办？"},
		[3]string{"How do I update the firmware of the %s?", "Where can I see the firmware version of the %s?", "The firmware update of my %s failed, what now?"},
		"打开配套应用，进入设置中的固件升级，电量需高于%d%%，升级期间请勿断电。",
		"Open the companion app, go to Settings, then Firmware. Keep the battery above %d%% during the update."},
	{[3]string{"%s怎么和手机蓝牙配对？", "%s搜索不到蓝牙信号怎么办？", "%s蓝牙总是断开是什么原因？"},
		[3]string{"How do I pair the %s with my phone over Bluetooth?", "My phone cannot find the %s in Bluetooth, why?", "The Bluetooth of my %s keeps disconnecting, what can I do?"},
		"长按电源键%d秒进入配对模式，指示灯蓝色闪烁后在手机蓝牙列表中选择设备。",
		"Hold the power button for %d seconds until the light blinks blue, then pick the device in your phone list."},
	{[3]string{"%s怎么恢复出厂设置？", "%s如何重置？", "%s恢复出厂设置会删除我的数据吗？"},
		[3]string{"How do I factory reset the %s?", "How can I reset my %s to default settings?", "Will a factory reset of the %s erase my data?"},
		"同时按住电源键和音量键%d秒，指示灯红色闪烁两次即表示已重置，本地设置将被清除。",
		"Hold the power and volume keys together for %d seconds. Two red blinks confirm the reset and local settings are cleared."},
	{[3]string{"%s的保修期是多久？", "%s保修包含哪些内容？", "%s过了保修期还能维修吗？"},
		[3]string{"How long is the warranty of the %s?", "What does the warranty of the %s cover?", "Can the %s still be repaired after the warranty ends?"},
		"%s自购买之日起提供%d个月有限保修，人为损坏和进水不在保修范围内。",
		"The warranty runs %d months from the purchase date. Accidental damage and water damage are not covered."},
	{[3]string{"%s可以退货吗？", "%s退货需要满足什么条件？", "%s退款多久能到账？"},
		[3]string{"Can I return the %s?", "What are the conditions to return the %s?", "How long does a refund for the %s take?"},
		"签收后%d天内商品完好、配件齐全可申请退货，退款在收到退货后三个工作日内处理。",
		"Returns are accepted within %d days of delivery if the item is intact. Refunds follow in three working days."},
	{[3]string{"买%s怎么开发票？", "%s的发票可以改抬头吗？", "%s电子发票在哪里下载？"},
		[3]string{"How do I get an invoice for the %s?", "Can I change the invoice title for my %s order?", "Where can I download the e-invoice for the %s?"},
		"在订单详情页点击申请发票，填写抬头和税号，提交后约%d个工作日内发送到您的邮箱。",
		"Open the order page and choose Request invoice. Enter the title and tax number and it is emailed within %d working days."},
	{[3]string{"%s下单后多久发货？", "%s的配送需要几天？", "%s可以修改收货地址吗？"},
		[3]string{"How soon does the %s ship after I order?", "How many days does delivery of the %s take?", "Can I change the delivery address of my %s order?"},
		"现货商品在下单后%d小时内发出，偏远地区可能延迟一到两天，发货前可修改地址。",
		"In-stock items ship within %d hours of the order. Remote areas may take one or two more days."},
	{[3]string{"%s怎么绑定到我的账号？", "%s可以解绑账号吗？", "%s换了新手机后如何重新绑定？"},
		[3]string{"How do I bind the %s to my account?", "Can I unbind the %s from my account?", "How do I link the %s again after changing phones?"},
		"登录应用后点击添加设备，扫描机身二维码即可绑定，每个设备最多同时绑定%d个账号。",
		"Sign in to the app, tap Add device and scan the code on the body. One device can be bound to %d accounts at most."},
	{[3]string{"%s的指示灯一直闪红灯是怎么回事？", "%s亮红灯代表什么？", "%s指示灯不亮怎么办？"},
		[3]string{"Why is the light of my %s blinking red?", "What does a red light on the %s mean?", "The indicator light of my %s does not turn on, why?"},
		"红灯闪烁表示电量低于%d%%或出现故障，请先充电；充电后仍闪烁请联系售后。",
		"A blinking red light means the battery is below %d%% or there is a fault. Charge it first, then contact support."},
	{[3]string{"%s防水吗？", "%s可以带着洗澡或游泳吗？", "%s进水了还能用吗？"},
		[3]string{"Is the %s waterproof?", "Can I wear the %s in the shower or swimming pool?", "Can I still use the %s after it got wet?"},
		"%s具备IP%d级防护，可抵御日常泼溅，但不建议浸泡或使用热水冲洗。",
		"The device has an IP%d rating that handles splashes. Soaking it or rinsing with hot water is not recommended."},
	{[3]string{"%s的电池能用多久？", "%s续航时间有多长？", "%s耗电太快怎么处理？"},
		[3]string{"How long does the battery of the %s last?", "What is the battery life of the %s?", "The %s drains its battery too fast, what can I do?"},
		"正常使用下续航约%d天，关闭常亮显示和后台同步可以延长使用时间。",
		"With normal use the battery lasts about %d days. Turning off the always-on display and background sync helps."},
}

var (
	zhPrefix = []string{"", "", "请问", "想问一下，", "你好，"}
	enPrefix = []string{"", "", "Hi, ", "Quick question: ", "Hello, "}
)

// render fills a template: %s is the product, %d a number, %% a percent sign.
func render(tpl, product string, n int) string {
	r := strings.NewReplacer("%%", "%", "%s", product, "%d", strconv.Itoa(n))
	return r.Replace(tpl)
}

// Generate builds the corpus: 60 queries and 200 passages, half per language.
// The same seed always gives the same corpus.
func Generate(seed uint64) *Workload {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	w := &Workload{}
	seen := map[string]bool{}
	gen := func(lang string, count int, withAnswer bool, out *[]Item) {
		for len(*out) < count {
			ti := rng.IntN(len(topics))
			tp := topics[ti]
			prod := products[rng.IntN(len(products))]
			qi := rng.IntN(3)
			num := 2 + rng.IntN(60)
			var q, a string
			if lang == "zh" {
				q = render(tp.zhQ[qi], prod, num)
				a = render(tp.zhA, prod, num)
				if !withAnswer {
					q = zhPrefix[rng.IntN(len(zhPrefix))] + q
				}
			} else {
				q = render(tp.enQ[qi], prod, num)
				a = render(tp.enA, prod, num)
				if !withAnswer {
					q = enPrefix[rng.IntN(len(enPrefix))] + q
				}
			}
			key := lang + "|" + fmt.Sprint(withAnswer) + "|" + q
			if withAnswer {
				key += "|" + a
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			it := Item{Lang: lang, Topic: ti, Product: prod, Q: q}
			if withAnswer {
				it.A = a
			}
			*out = append(*out, it)
		}
	}
	var zq, eq, zp, ep []Item
	gen("zh", 30, false, &zq)
	gen("en", 30, false, &eq)
	gen("zh", 100, true, &zp)
	gen("en", 100, true, &ep)
	w.Queries = append(append(w.Queries, zq...), eq...)
	w.Passages = append(append(w.Passages, zp...), ep...)
	return w
}

// Text returns the passage text for a rerank variant: "q" is the question
// only, "qa" is question and answer.
func (it Item) Text(variant string) string {
	if variant == "qa" {
		return it.Q + "\n" + it.A
	}
	return it.Q
}

// PassagesOf returns the passages of one language.
func (w *Workload) PassagesOf(lang string) []Item {
	var out []Item
	for _, p := range w.Passages {
		if p.Lang == lang {
			out = append(out, p)
		}
	}
	return out
}

// CandidateSet picks n passages of the query's language for the i-th rerank
// call: a sliding window over the language's passages, so each call differs
// but is reproducible.
func (w *Workload) CandidateSet(q Item, i, n int) []Item {
	ps := w.PassagesOf(q.Lang)
	out := make([]Item, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, ps[(i*7+k)%len(ps)])
	}
	return out
}

// FingerprintSentences returns the fixed 50 sentences (25 per language) whose
// embeddings are compared across hosts.
func (w *Workload) FingerprintSentences() []string {
	var out []string
	for _, lang := range []string{"zh", "en"} {
		ps := w.PassagesOf(lang)
		for k := 0; k < 25; k++ {
			p := ps[k*4%len(ps)]
			out = append(out, p.Q+" "+p.A)
		}
	}
	return out
}

// RerankFingerprint is one fixed (query, passages) set for score comparison.
type RerankFingerprint struct {
	Query    string   `json:"query"`
	Passages []string `json:"passages"`
}

// FingerprintRerankSets returns four fixed sets (two per language) of ten
// question+answer passages. The first passages share the query's topic, so
// the expected order has a clear head.
func (w *Workload) FingerprintRerankSets() []RerankFingerprint {
	var out []RerankFingerprint
	for _, lang := range []string{"zh", "en"} {
		var qs []Item
		for _, q := range w.Queries {
			if q.Lang == lang {
				qs = append(qs, q)
			}
		}
		for s := 0; s < 2; s++ {
			q := qs[s*5]
			ps := w.PassagesOf(lang)
			var set []Item
			used := map[int]bool{}
			for i, p := range ps {
				if p.Topic == q.Topic && len(set) < 3 {
					set = append(set, p)
					used[i] = true
				}
			}
			for i := s * 11; len(set) < 10; i++ {
				idx := i % len(ps)
				if !used[idx] && ps[idx].Topic != q.Topic {
					set = append(set, ps[idx])
					used[idx] = true
				}
			}
			fp := RerankFingerprint{Query: q.Q}
			for _, p := range set {
				fp.Passages = append(fp.Passages, p.Text("qa"))
			}
			out = append(out, fp)
		}
	}
	return out
}
