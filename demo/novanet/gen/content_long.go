// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"strings"
)

// Long handbooks and short single-answer pages. In a handbook most sections are
// a few sentences; some are long (well under the 6000 character cut) and hold
// one answer sentence among ordinary background, so retrieval has to find the
// section and the model has to find the sentence.

type topic struct {
	headEN, headZH string
	keyEN, keyZH   string
	long           bool
}

var fillerEN = []string{
	"We aim to treat every customer fairly, politely and without delay.",
	"Our staff are trained to explain each charge and each condition in plain words.",
	"If something is unclear, you can always ask a member of staff to go through it again.",
	"Most requests can be completed in the NovaNet app without a visit to a service hall.",
	"Please keep your account number and ID at hand, because we need them to confirm who you are.",
	"Rules in this handbook apply to every plan unless the plan sheet says otherwise.",
	"Opening hours and details of a service hall can differ, so check the hall page before you go.",
	"We review this handbook twice a year and tell customers about changes that affect them.",
	"A request made on a public holiday is handled on the next business day.",
	"If a request needs a document, you can photograph it and send it in the app.",
	"Where a form is needed, the staff member fills it in with you and gives you a copy.",
	"We keep notes of each contact so that you do not have to repeat yourself next time.",
}

var fillerZH = []string{
	"我们力求公平、礼貌、及时地对待每一位客户。",
	"我们的工作人员经过培训，会用通俗的话向您解释每一项费用和条件。",
	"如果有不清楚的地方，随时可以请工作人员再讲解一遍。",
	"大多数业务都可以在星联宽带应用里完成，不必专门跑一趟营业厅。",
	"请准备好账户号码和证件，我们需要用它们确认您的身份。",
	"除非套餐资费说明另有规定，本手册的规则适用于所有套餐。",
	"各营业厅的营业时间和细节可能不同，前往之前请查看营业厅页面。",
	"我们每年两次审阅本手册，并把影响客户的变化告知大家。",
	"法定节假日提出的申请，顺延到下一个工作日处理。",
	"需要提交材料的，可以拍照后通过应用发送。",
	"需要填表的，由工作人员与您一起填写，并交给您一份副本。",
	"我们会记录每一次联系，这样下次您不必重复说明。",
}

// longSection builds a section body: sentences of background around the key
// sentence, placed somewhere in the middle.
func longSection(pool []string, key string, seed int, sep string) string {
	n := len(pool)
	var parts []string
	for i := 0; i < 6; i++ {
		parts = append(parts, pool[(seed+i*5)%n])
	}
	parts = append(parts, key)
	for i := 0; i < 8; i++ {
		parts = append(parts, pool[(seed+3+i*7)%n])
	}
	return strings.Join(parts, sep)
}

func handbook(title, titleZH, zhName, enName string, topics []topic) {
	var en, zh strings.Builder
	fmt.Fprintf(&en, "# %s\nVersion: 2026.2\nOwner: Customer Care\n", title)
	fmt.Fprintf(&zh, "# %s\n版本：2026.2\n负责部门：客户服务部\n", titleZH)
	for i, t := range topics {
		fmt.Fprintf(&en, "## %s\n", t.headEN)
		fmt.Fprintf(&zh, "## %s\n", t.headZH)
		if t.long {
			fmt.Fprintf(&en, "%s\n", longSection(fillerEN, t.keyEN, i, " "))
			fmt.Fprintf(&zh, "%s\n", longSection(fillerZH, t.keyZH, i, ""))
		} else {
			fmt.Fprintf(&en, "%s\n", t.keyEN)
			fmt.Fprintf(&zh, "%s\n", t.keyZH)
		}
	}
	nvPairNamed(enName, zhName, en.String(), zh.String())
}

func init() {
	handbook("Customer Service Handbook", "客户服务手册", "handbooks/客户服务手册_2026", "handbooks/customer-service-handbook", []topic{
		{"Welcome", "欢迎", "This handbook explains how NovaNet serves its customers and what you can expect from us.", "本手册说明星联宽带如何服务客户，以及您可以对我们有什么期待。", false},
		{"Contacting customer care", "联系客服", "Customer care by phone is open every day from 8:00 to 22:00, and chat in the NovaNet app is open around the clock.", "客服电话每天8:00至22:00服务，应用内在线客服全天24小时服务。", false},
		{"Languages", "服务语言", "Customer care speaks Chinese and English.", "客服提供中文和英文服务。", false},
		{"Opening an account", "开户", "To open an account you need a valid ID, a contact number and an address for broadband.", "开户需要有效证件、联系电话，办理宽带还需要提供地址。", false},
		{"Choosing a plan", "选择套餐", "The plan sheets list the fee, the allowances and the contract of every plan. A staff member can help you compare them.", "各套餐的资费说明列出月费、权益和合约期，工作人员可以帮您对比。", false},
		{"Equipment warranty", "设备保修", "Routers and optical modems supplied by NovaNet are covered by a 24-month warranty from the installation date.", "星联宽带提供的路由器和光猫，自安装之日起享受24个月保修。", true},
		{"Moving your service", "搬家移机", "A relocation request must be made at least 7 days before the move so that we can book the technician.", "移机申请须至少在搬家前7天提出，以便安排师傅。", true},
		{"Call recording and privacy", "通话录音与隐私", "Recordings of calls to customer care are kept for 90 days and then deleted.", "客服通话录音保存90天，之后删除。", true},
		{"Technician visits", "师傅上门", "Every technician carries a NovaNet ID card with a photo and a QR code that you can scan in the app to check who they are.", "每位师傅都带有星联宽带工作证，证件上有照片和二维码，可以用应用扫描核实身份。", true},
		{"Accessibility", "无障碍服务", "Large-print bills and a call-back service are available on request at no charge.", "大字版账单和回拨服务可按需申请，不收费。", true},
		{"Lost or stolen phone", "手机丢失或被盗", "A line reported lost or stolen is blocked within 10 minutes of the report.", "报失的号码会在报告后10分钟内停止服务。", true},
		{"Scam warning", "防诈骗提示", "NovaNet staff never ask for your SMS code, password or card PIN, so never give them to anyone who calls you.", "星联宽带工作人员绝不会向您索要短信验证码、密码或银行卡密码，请不要告诉任何来电者。", true},
		{"Closing an account", "销户", "An account with a zero balance is closed within 3 business days after the request.", "余额为零的账户，在申请后3个工作日内完成销户。", true},
		{"Feedback", "意见反馈", "You can send feedback in the app under Help, or tell any member of staff.", "您可以在应用的“帮助”里提交意见，也可以告诉任何一位工作人员。", false},
		{"Service halls", "营业厅", "Service halls handle payments, SIM replacement, number porting and device pickup. Each hall page lists its own hours.", "营业厅办理缴费、补卡、携号转网和设备领取，各营业厅页面列出各自的营业时间。", false},
		{"Appointments", "预约", "Halls take walk-ins, and you can also book a time slot in the app to avoid waiting.", "营业厅接待直接到访的客户，也可以在应用里预约时段，避免排队。", false},
		{"Queue numbers", "取号", "Queue numbers are issued at the entrance and are valid for the day.", "排队号在入口领取，当天有效。", false},
		{"Payment at the hall", "营业厅缴费", "Payments at a hall can be made in cash or by card.", "在营业厅可以用现金或银行卡缴费。", false},
		{"Documents we accept", "可接受的证件", "We accept national ID cards, passports and residence permits.", "我们接受居民身份证、护照和居留许可。", false},
		{"Power of attorney", "委托代办", "Someone else can act for you with a signed letter and both IDs.", "他人代办需要提供签字的委托书和双方证件。", false},
		{"Children and minors", "未成年人", "Minors cannot be account holders. A parent or guardian opens the account.", "未成年人不能作为账户持有人，由父母或监护人开户。", false},
		{"Foreign visitors", "外籍访客", "Visitors can buy a prepaid SIM with a passport.", "访客凭护照可以购买预付费SIM卡。", false},
		{"Prepaid SIMs", "预付费SIM卡", "A prepaid SIM works for 12 months after the last top-up.", "预付费SIM卡在最后一次充值后12个月内有效。", false},
		{"Language of documents", "文件语言", "Contracts and bills are available in Chinese and English.", "合同和账单提供中文和英文版本。", false},
		{"Data we keep", "我们保存的资料", "We keep the data needed to run your service and to meet legal duties, and no more.", "我们只保存运营服务和履行法定义务所需的资料。", false},
		{"Marketing messages", "营销短信", "You can stop marketing messages by replying STOP or by switching them off in the app.", "回复TD或者在应用里关闭，即可停止营销短信。", false},
		{"Surveys", "满意度调查", "After a contact you may receive a short survey. It takes under a minute.", "联系结束后您可能收到简短的满意度调查，不到一分钟即可完成。", false},
		{"Staff conduct", "员工行为准则", "Staff may not accept gifts or ask for personal favors.", "员工不得收受礼品，也不得提出个人请求。", false},
		{"Changes to this handbook", "手册的修订", "Changes take effect on the date printed on the new version.", "修订自新版本上印刷的日期起生效。", false},
		{"Glossary", "术语表", "A bill date is the day a bill is issued. A due date is the last day to pay without a late fee.", "账单日是出账单的日子，到期日是免收滞纳金的最后缴费日。", false},
	})

	handbook("Home Network Handbook", "家庭网络手册", "handbooks/家庭网络手册", "handbooks/home-network-handbook", []topic{
		{"About this handbook", "关于本手册", "This handbook helps you plan and run the home network that comes with a NovaNet broadband plan.", "本手册帮助您规划和使用随星联宽带套餐提供的家庭网络。", false},
		{"Where to put the router", "路由器放在哪里", "Put the router in the middle of the home, up on a shelf and away from metal and microwave ovens.", "把路由器放在家中间、较高的架子上，远离金属物品和微波炉。", false},
		{"Wi-Fi channels", "Wi-Fi信道", "The router picks the best Wi-Fi channel by itself every night.", "路由器每天夜里自动选择最佳的Wi-Fi信道。", false},
		{"Guest Wi-Fi", "访客Wi-Fi", "A guest network keeps visitors away from your own devices. Switch it on in the app.", "访客网络可以让客人与您自己的设备隔离，在应用里即可开启。", true},
		{"Parental controls", "家长控制", "Parental controls can block sites and set bedtime hours for each child device. They are free.", "家长控制可以屏蔽网站，并为每个孩子的设备设置休息时间，免费使用。", true},
		{"Port forwarding", "端口转发", "Port forwarding for a camera or a game console is set in the app. Fiber plans use a shared public address, so forwarding from outside the home is not supported.", "摄像头或游戏机的端口转发可以在应用里设置。光纤套餐使用共享公网地址，不支持从外部访问家里的设备。", true},
		{"Fixed IP address", "固定IP地址", "Home plans do not include a fixed IP address. Business broadband does.", "家庭套餐不含固定IP地址，企业宽带包含。", false},
		{"IPv6", "IPv6", "IPv6 is switched on by default on all routers.", "所有路由器默认开启IPv6。", false},
		{"Using your own router", "使用自己的路由器", "You may use your own router behind the NovaNet modem, but we can only support NovaNet routers.", "您可以在星联宽带光猫后面使用自己的路由器，但我们只能为星联宽带路由器提供支持。", true},
		{"Wired connections", "有线连接", "A network cable gives the most stable connection for a TV box, a desktop computer or a game console.", "电视盒、台式电脑或游戏机用网线连接最稳定。", false},
		{"Power cuts", "停电", "After a power cut the modem and the router restart by themselves. Allow 5 minutes.", "停电恢复后光猫和路由器会自动重启，请等待5分钟。", false},
		{"Smart home devices", "智能家居设备", "Most smart home devices use the 2.4 GHz network. Keep it switched on.", "大多数智能家居设备使用2.4GHz网络，请保持开启。", false},
		{"Video calls", "视频通话", "For clear video calls use the 5 GHz network or a cable.", "想要清晰的视频通话，请使用5GHz网络或网线。", false},
		{"Gaming", "游戏", "A cable and the R5 Pro give the lowest delay for gaming.", "网线加R5 Pro可以获得最低的游戏延迟。", false},
		{"Security", "安全", "Change the default Wi-Fi password if you have shared it widely.", "如果默认的Wi-Fi密码已经广为人知，请修改。", false},
		{"Cleaning the equipment", "设备清洁", "Wipe the equipment with a dry cloth. Never use water or spray cleaners.", "用干布擦拭设备，不要用水或喷雾清洁剂。", false},
		{"Moving the router", "移动路由器", "Moving the router to another room is fine, but do not move the optical modem, because the fiber must not be bent.", "路由器可以挪到别的房间，但不要移动光猫，因为光纤不能弯折。", false},
		{"Fiber cable care", "光纤保护", "The fiber is thin and breaks if it is bent sharply. Keep a bend radius of at least 3 centimeters.", "光纤很细，急弯会断，弯曲半径请保持在3厘米以上。", false},
		{"Wall sockets", "墙面插座", "Ask the technician to place the modem next to a socket so that no extension is needed.", "请师傅把光猫装在插座旁边，这样不需要拖线板。", false},
		{"Electrical safety", "用电安全", "Do not cover the equipment. It needs air to keep cool.", "不要遮盖设备，它需要通风散热。", false},
	})

	handbook("Mobile Services Handbook", "手机业务手册", "handbooks/手机业务手册", "handbooks/mobile-services-handbook", []topic{
		{"About this handbook", "关于本手册", "This handbook covers the everyday use of a NovaNet mobile line.", "本手册介绍星联宽带手机号码的日常使用。", false},
		{"Your SIM card", "您的SIM卡", "NovaNet SIM cards are 3-in-1, so one card fits standard, micro and nano slots.", "星联宽带的SIM卡是三合一卡，一张卡适用于标准、Micro和Nano卡槽。", false},
		{"PIN and PUK", "PIN码和PUK码", "The default PIN is printed on the SIM card holder. After 3 wrong tries the PUK code is needed, and it is also printed on the holder.", "默认PIN码印在SIM卡卡托上。输错3次后需要PUK码，PUK码也印在卡托上。", true},
		{"Network modes", "网络制式", "The phone chooses 5G, 4G or 3G by itself. Basic and Senior Care work on 4G at most.", "手机会自动选择5G、4G或3G。基础套餐和敬老套餐最高使用4G网络。", false},
		{"Voice over Wi-Fi", "Wi-Fi通话", "Wi-Fi calling is free on every plan, and you can switch it on in the phone settings.", "所有套餐的Wi-Fi通话都免费，在手机设置里开启即可。", true},
		{"Data saver", "省流量模式", "A phone data saver can reduce data use by a third. We cannot turn it on for you.", "手机的省流量模式可以减少约三分之一的流量使用，我们不能代您开启。", true},
		{"Voicemail setup", "语音信箱设置", "Voicemail is on by default. You can change the greeting in the app.", "语音信箱默认开启，可以在应用里修改问候语。", false},
		{"Call forwarding", "呼叫转移", "Call forwarding is set in the phone settings and costs nothing extra.", "呼叫转移在手机设置里设置，不另外收费。", false},
		{"Blocking numbers", "拦截号码", "Block a number on the phone or report spam in the app.", "可以在手机上拦截号码，或在应用里举报骚扰电话。", false},
		{"Multi-SIM phones", "双卡手机", "In a dual-SIM phone, pick the NovaNet SIM for mobile data in the SIM settings.", "双卡手机请在SIM卡设置里选择用星联宽带的卡上网。", false},
		{"Hotspot sharing", "热点共享", "Hotspot sharing is available on Plan 59, Plan 59 Plus and Plan 99.", "59元套餐、59元Plus套餐和99元套餐支持热点共享。", false},
		{"Data speed after the limit", "流量用完后的速率", "After the monthly data is used up, the speed falls to 1 Mbps until the next month or until you buy a Booster pack.", "当月流量用完后，速率降到1Mbps，直到下月或购买加油包。", false},
		{"International numbers", "国际号码", "Calling abroad from a NovaNet line costs per minute and depends on the country. The rate list is in the app.", "用星联宽带号码拨打国际长途按分钟计费，因国家而异，费率表在应用里。", false},
		{"Emergency calls", "紧急呼叫", "Emergency calls are free and work even without credit or a SIM.", "紧急呼叫免费，即使没有余额或没有SIM卡也可以拨打。", false},
		{"Roaming at home", "境内漫游", "There is no extra charge for using your line anywhere in the country.", "在国内任何地方使用号码都不另外收费。", false},
		{"Satellite and in-flight use", "卫星和机上使用", "NovaNet lines do not work in flight. Switch on airplane mode.", "星联宽带号码在飞行途中无法使用，请开启飞行模式。", false},
		{"Software updates", "系统更新", "Update the phone software over Wi-Fi to save data.", "请通过Wi-Fi更新手机系统，以节省流量。", false},
		{"Battery tips", "省电提示", "Turn off 5G in areas with poor signal to save battery.", "信号差的地方关闭5G可以省电。", false},
	})
}
