// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"strings"
)

// The two policies that changed. Each has an old and a new version as separate
// documents, with contradicting terms. Every changed term states its effective
// period in its own text, so an answer drawn from one section carries the
// dates. The old versions also cover a topic the new ones dropped (the renewal
// gift, paper bills).

// feeRows renders the early termination table of the mobile and broadband
// contract plans; old maps a plan key to the fee of the old policy.
func feeRows(old map[string]string, zh bool) string {
	var sb strings.Builder
	for _, k := range []string{"Plan 59", "Plan 59 Plus", "Plan 99", "Fiber 500", "Fiber 1000"} {
		p := nvPlan(k)
		fee := yuan(p.EarlyTermFee)
		if old != nil {
			fee = old[k]
		}
		name := p.NameEN
		if zh {
			name = p.NameZH
		}
		fmt.Fprintf(&sb, "| %s | %s |\n", name, fee)
	}
	return sb.String()
}

var oldTermFees = map[string]string{"Plan 59": "150", "Plan 59 Plus": "300", "Plan 99": "400", "Fiber 500": "300", "Fiber 1000": "600"}

func init() {
	// ------------------------------------------------ contract policy, old
	nvPairNamed("policies/contract-and-early-termination-policy-v2", "policies/合约与提前解约政策_2024版",
		`# Contract Policy, version 2
Effective: 1 July 2024 to 30 June 2026
Status: replaced by version 3 on 1 July 2026
## Cooling-off period
From 1 July 2024 to 30 June 2026 the cooling-off period is 7 days. A new customer who cancels within 7 days of activation pays no early termination fee.
## Early termination fee
From 1 July 2024 to 30 June 2026 the early termination fee depends on the plan, as the table shows.
| Plan | Early termination fee (yuan) |
`+feeRows(oldTermFees, false)+`Basic and Senior Care have no contract, so they have no early termination fee.
## How the fee changes over the contract
From 1 July 2024 to 30 June 2026 the fee is charged in full, however many months of the contract are left.
## Moving house
From 1 July 2024 to 30 June 2026 a broadband customer gets one free relocation during the contract. A second relocation costs 100 yuan.
## Plan downgrade during the contract
From 1 July 2024 to 30 June 2026 a plan cannot be downgraded before the contract ends. Upgrades are allowed at any time.
## Renewal gift
From 1 July 2024 to 30 June 2026 a customer who renews within 60 days before the contract ends gets a loyalty gift. A mobile customer gets 10 GB of extra data a month for 6 months. A broadband customer gets one free router upgrade.
## How to give notice
To cancel, tell us in the NovaNet app or at a service hall. The cancellation takes effect 30 days after the request.
`,
		`# 合约与提前解约政策（第二版）
生效期间：2024年7月1日至2026年6月30日
状态：2026年7月1日起由第三版取代
## 冷静期
2024年7月1日至2026年6月30日期间，冷静期为7天。新客户在开通后7天内取消，不收提前解约费。
## 提前解约费
2024年7月1日至2026年6月30日期间，提前解约费按套餐收取，见下表。
| 套餐 | 提前解约费（元） |
`+feeRows(oldTermFees, true)+`基础套餐和敬老套餐没有合约期，不收提前解约费。
## 解约费随合约递减吗
2024年7月1日至2026年6月30日期间，无论合约还剩几个月，提前解约费一律全额收取。
## 搬家移机
2024年7月1日至2026年6月30日期间，宽带客户在合约期内可免费移机一次，第二次移机收费100元。
## 合约期内降档
2024年7月1日至2026年6月30日期间，合约期内不能降档，升档不受限制。
## 续约赠礼
2024年7月1日至2026年6月30日期间，合约到期前60天内续约的客户可获得忠诚赠礼：手机客户连续6个月每月多送10GB流量，宽带客户免费升级一次路由器。
## 如何提出解约
取消套餐请在星联宽带应用里提出，或到营业厅办理。解约自申请之日起30天后生效。
`)

	// ------------------------------------------------ contract policy, new
	nvPairNamed("policies/contract-and-early-termination-policy-v3", "policies/合约与提前解约政策_2026版",
		`# Contract Policy, version 3
Effective: from 1 July 2026
Replaces: version 2
## Cooling-off period
From 1 July 2026 the cooling-off period is 15 days. A new customer who cancels within 15 days of activation pays no early termination fee.
## Early termination fee
From 1 July 2026 the early termination fee depends on the plan, as the table shows.
| Plan | Early termination fee (yuan) |
`+feeRows(nil, false)+`Basic and Senior Care have no contract, so they have no early termination fee.
## How the fee changes over the contract
From 1 July 2026 the fee falls in equal steps every month of the contract. A customer who has served half of the contract pays half of the fee.
## Moving house
From 1 July 2026 a broadband customer gets two free relocations during the contract. A third relocation costs 100 yuan.
## Plan downgrade during the contract
From 1 July 2026 a customer can downgrade once every 12 months. The new plan starts on the next bill date and the contract end date does not change.
## How to give notice
To cancel, tell us in the NovaNet app or at a service hall. The cancellation takes effect 30 days after the request.
`,
		`# 合约与提前解约政策（第三版）
生效期间：2026年7月1日起
状态：取代第二版
## 冷静期
自2026年7月1日起，冷静期为15天。新客户在开通后15天内取消，不收提前解约费。
## 提前解约费
自2026年7月1日起，提前解约费按套餐收取，见下表。
| 套餐 | 提前解约费（元） |
`+feeRows(nil, true)+`基础套餐和敬老套餐没有合约期，不收提前解约费。
## 解约费随合约递减吗
自2026年7月1日起，提前解约费按合约月数等额递减。合约已履行一半的客户，只需支付一半的提前解约费。
## 搬家移机
自2026年7月1日起，宽带客户在合约期内可免费移机两次，第三次移机收费100元。
## 合约期内降档
自2026年7月1日起，客户每12个月可以降档一次，新套餐从下个账单日起生效，合约到期日不变。
## 如何提出解约
取消套餐请在星联宽带应用里提出，或到营业厅办理。解约自申请之日起30天后生效。
`)

	// ------------------------------------------------ late payment, old
	nvPairNamed("policies/late-payment-and-suspension-policy-v1", "policies/逾期缴费与停机政策_2025版",
		`# Late Payment Policy, version 1
Effective: 1 March 2025 to 31 August 2026
Status: replaced by version 2 on 1 September 2026
## When is a bill due?
A bill is due on the 20th day after the bill date.
## Grace period
From 1 March 2025 to 31 August 2026 the grace period is 7 days after the due date. No late fee is charged during the grace period.
## Late fee
From 1 March 2025 to 31 August 2026 the late fee is 2 percent of the overdue amount, charged once when the grace period ends.
## When is service suspended?
From 1 March 2025 to 31 August 2026 outgoing calls and mobile data are suspended 15 days after the due date. Incoming calls still work.
## How fast is service restored?
From 1 March 2025 to 31 August 2026 service is restored within 24 hours after the overdue amount is paid.
## Paper bills
From 1 March 2025 to 31 August 2026 a customer can ask for a paper bill by post. A paper bill costs 5 yuan per month.
`,
		`# 逾期缴费与停机政策（第一版）
生效期间：2025年3月1日至2026年8月31日
状态：2026年9月1日起由第二版取代
## 账单什么时候到期？
账单自账单日起第20天到期。
## 宽限期
2025年3月1日至2026年8月31日期间，到期日后有7天宽限期，宽限期内不收滞纳金。
## 滞纳金
2025年3月1日至2026年8月31日期间，滞纳金为逾期金额的百分之二，宽限期结束时收取一次。
## 什么时候停机？
2025年3月1日至2026年8月31日期间，到期日后第15天起暂停呼出电话和手机流量，仍可接听来电。
## 多久恢复服务？
2025年3月1日至2026年8月31日期间，缴清欠费后24小时内恢复服务。
## 纸质账单
2025年3月1日至2026年8月31日期间，客户可以申请邮寄纸质账单，每月收费5元。
`)

	// ------------------------------------------------ late payment, new
	nvPairNamed("policies/late-payment-and-suspension-policy-v2", "policies/逾期缴费与停机政策_2026版",
		`# Late Payment Policy, version 2
Effective: from 1 September 2026
Replaces: version 1
## When is a bill due?
A bill is due on the 20th day after the bill date.
## Grace period
From 1 September 2026 the grace period is 10 days after the due date. No late fee is charged during the grace period.
## Late fee
From 1 September 2026 the late fee is 1 percent of the overdue amount, charged once when the grace period ends.
## When is service suspended?
From 1 September 2026 outgoing calls and mobile data are suspended 20 days after the due date. Incoming calls still work.
## How fast is service restored?
From 1 September 2026 service is restored within 2 hours after the overdue amount is paid.
`,
		`# 逾期缴费与停机政策（第二版）
生效期间：2026年9月1日起
状态：取代第一版
## 账单什么时候到期？
账单自账单日起第20天到期。
## 宽限期
自2026年9月1日起，到期日后有10天宽限期，宽限期内不收滞纳金。
## 滞纳金
自2026年9月1日起，滞纳金为逾期金额的百分之一，宽限期结束时收取一次。
## 什么时候停机？
自2026年9月1日起，到期日后第20天起暂停呼出电话和手机流量，仍可接听来电。
## 多久恢复服务？
自2026年9月1日起，缴清欠费后2小时内恢复服务。
`)
}

// Policies that did not change.
func init() {
	nvPair("policies/fair-use-policy",
		`# Fair Use Policy
Applies to: all plans
## Is there a data cap on home broadband?
Home broadband has no monthly data cap. If a household uses more than 3 TB in a month, the speed may be reduced to 100 Mbps until the end of that month.
## What happens when mobile data runs out?
When the monthly data runs out, mobile data is slowed to 1 Mbps. You can buy a Booster pack to get full speed again.
## Can I run a server at home?
Home broadband is for personal use. Running a public server or reselling the connection is not allowed.
## Are calls limited?
Voice minutes beyond the plan allowance cost 0.20 yuan per minute.
`,
		`# 合理使用政策
适用范围：全部套餐
## 家庭宽带有流量上限吗？
家庭宽带没有每月流量上限。如果一个家庭一个月用量超过3TB，当月剩余时间速率可能降到100Mbps。
## 手机流量用完会怎样？
当月流量用完后，手机上网速率降到1Mbps，购买加油包可以恢复全速。
## 可以在家里的宽带上架设服务器吗？
家庭宽带仅供个人使用，不得架设对外服务器，也不得转售。
## 通话有限制吗？
超出套餐的通话分钟数，每分钟收费0.20元。
`)

	nvPair("policies/complaints-process",
		`# How to make a complaint
Owner: Customer Care
## How do I make a complaint?
You can complain in the NovaNet app under Help, at any service hall, or by calling customer care. Please have your account number ready.
## How fast will my complaint be answered?
We acknowledge a complaint within 24 hours and aim to resolve it within 10 business days.
## What if I am not happy with the answer?
Ask for escalation. A senior case manager reviews the complaint and replies within 5 business days. If you still disagree, you can contact the telecom regulator.
## Can I complain for someone else?
Yes, if the account holder gives written permission and you show both IDs.
`,
		`# 投诉处理流程
负责部门：客户服务部
## 怎么投诉？
可以在星联宽带应用的帮助页面投诉，也可以到任意营业厅投诉，或拨打客服电话。请准备好账户号码。
## 投诉多久有回复？
我们在24小时内确认收到投诉，并力争在10个工作日内处理完毕。
## 对处理结果不满意怎么办？
可以要求升级处理，高级个案经理会复核投诉，并在5个工作日内回复。仍有异议的，可以向通信管理部门反映。
## 可以替别人投诉吗？
可以，需要账户持有人出具书面授权，并出示双方证件。
`)

	nvPair("policies/refunds-and-credits",
		`# Refunds and credits
## When can I get a refund?
A refund is possible for a duplicate payment, a charge made after cancellation, or a service fault that lasted more than 48 hours.
## How long does a refund take?
An approved refund is paid back to the original payment method within 7 business days.
## Are roaming packs refundable?
A roaming pack that has not been used can be refunded in full. A pack that has started cannot be refunded.
## Are device purchases refundable?
A new device can be returned within 14 days if it is unused and in the original box.
`,
		`# 退款与返还
## 什么情况可以退款？
重复扣款、取消服务后仍被扣费，或者服务故障持续超过48小时，都可以申请退款。
## 退款需要多久？
审核通过的退款在7个工作日内原路退回。
## 漫游包可以退吗？
未使用的漫游包可以全额退款，已经开始使用的漫游包不能退款。
## 购买的设备可以退吗？
全新未使用、包装完好的设备可在14天内退货。
`)

	nvPair("policies/number-porting-policy",
		`# Number porting policy
Applies to: mobile numbers
## Can I bring my number to NovaNet?
Yes. You can port your number in for free. Request it in the NovaNet app with your ID and the SMS code sent to the number.
## How long does porting in take?
Porting in takes 1 to 3 business days. Your old service stops when the port completes.
## What stops a number from being ported?
A number cannot be ported while it has an unpaid bill with the old carrier, or if the person asking is not the registered owner.
## Can I port my number out?
Yes. Ask in the NovaNet app or at a service hall. The number is released within 2 business days if the account has no unpaid bill.
## Does porting cancel my contract?
No. If you leave NovaNet during a contract, the early termination fee still applies.
`,
		`# 携号转网政策
适用范围：手机号码
## 可以把号码转到星联宽带吗？
可以，携号转入免费。请在星联宽带应用里提交申请，并提供证件和发送到该号码的短信验证码。
## 携号转入要多久？
携号转入需要1到3个工作日，转入完成时原运营商的服务停止。
## 哪些情况不能转？
号码在原运营商处有未结清账单，或者申请人不是登记机主，都不能转。
## 可以把星联宽带的号码转到别家吗？
可以。在星联宽带应用或营业厅申请，账户没有欠费的，2个工作日内释放号码。
## 转出会取消合约吗？
不会。合约期内转出，提前解约费照常收取。
`)

	nvPair("policies/sim-and-esim-policy",
		`# SIM and eSIM policy
## How much does a replacement SIM cost?
A replacement SIM costs 20 yuan. It is free if the SIM stopped working within 12 months of activation.
## What should I do if I lose my SIM?
Suspend the line right away in the NovaNet app or by calling customer care. Then collect a replacement at a service hall with your ID.
## How do I move my eSIM to a new phone?
Request an eSIM transfer in the NovaNet app. The QR code is valid for 24 hours and can be used on one phone. You can transfer an eSIM up to 3 times in 30 days.
## Which phones support eSIM?
Recent phones from most makers support eSIM. The list in the NovaNet app shows the models we have tested.
## Can every plan use eSIM?
No. Basic and Senior Care are only issued on physical SIM cards.
`,
		`# SIM卡与eSIM政策
## 补办SIM卡多少钱？
补办SIM卡20元。开通后12个月内SIM卡自行失效的，免费补办。
## SIM卡丢了怎么办？
请立即在星联宽带应用或拨打客服电话挂失停机，再带身份证到营业厅领取补办的SIM卡。
## eSIM怎么换到新手机？
在星联宽带应用里申请eSIM转移，二维码24小时内有效，只能在一台手机上使用。30天内最多转移3次。
## 哪些手机支持eSIM？
大多数品牌近年发布的手机支持eSIM，星联宽带应用里的列表是我们测试过的型号。
## 所有套餐都能用eSIM吗？
不是。基础套餐和敬老套餐只提供实体SIM卡。
`)

	nvPair("policies/extra-sim-and-family-sharing",
		`# Extra SIMs and family sharing
## What is an extra SIM?
An extra SIM is a second SIM card on the same account. It shares the data of the main SIM.
## Which plans include extra SIMs?
Plan 99 includes two extra SIMs at no charge. On Plan 59 Plus you can add one extra SIM for 10 yuan per month.
## Who can I add as a family member?
You can add relatives who live with you. The account holder is responsible for all charges.
## Can an extra SIM have its own number?
Yes. Every extra SIM has its own phone number.
`,
		`# 副卡与家庭共享
## 什么是副卡？
副卡是同一账户下的第二张SIM卡，与主卡共享流量。
## 哪些套餐含副卡？
99元套餐赠送两张副卡。59元Plus套餐可以加办一张副卡，每月10元。
## 可以添加哪些家人？
可以添加同住的亲属，账户持有人对所有费用负责。
## 副卡有独立号码吗？
有。每张副卡都有自己的手机号码。
`)

	nvPair("policies/id-verification-policy",
		`# ID verification
## Which ID do I need to sign up?
Sign-up needs a valid government ID of the account holder. A passport is accepted for foreign residents.
## What is the minimum age?
The account holder must be at least 18 years old. Senior Care needs the holder to be 65 or older.
## Can someone else collect my SIM?
Yes, with your signed permission, your ID copy and their own ID.
`,
		`# 实名认证
## 办理业务需要什么证件？
办理需要账户持有人的有效身份证件，外籍居民可以使用护照。
## 开户的最低年龄是多少？
账户持有人必须年满18周岁。敬老套餐要求持有人年满65周岁。
## 可以请别人代领SIM卡吗？
可以，需要您签字的授权书、您的证件复印件和代办人自己的证件。
`)

	nvPair("policies/service-outage-compensation",
		`# Service outage compensation
## When is outage compensation paid?
If your home broadband is down for more than 24 hours because of a NovaNet fault, we credit one day of the monthly fee for each full day of outage.
## Do I need to claim it?
No. The credit appears on the next bill. If it is missing, ask customer care.
## What is not covered?
Outages caused by your own equipment, by power cuts at your home, or by work you asked us to do are not compensated.
`,
		`# 服务中断补偿
## 服务中断可以得到什么补偿？
家庭宽带因星联宽带的故障中断超过24小时的，每满一天按一天月费返还。
## 需要自己申请吗？
不需要，补偿会在下期账单里体现。如果没有看到，请联系客服。
## 哪些情况不补偿？
因客户自己的设备、家里停电或客户要求的施工造成的中断，不予补偿。
`)

	nvPair("policies/equipment-return-policy",
		`# Equipment return
## Must I return the router on cancel?
Yes. The router that came with a broadband plan must be returned within 30 days after cancellation. Keeping it costs 300 yuan for the R3 and 600 yuan for the R5 Pro.
## How do I return equipment?
Bring it to any service hall, or book a free pickup in the NovaNet app.
## What if the equipment is damaged?
Normal wear is fine. Damage caused by water or by opening the case is charged at the replacement price.
`,
		`# 设备归还
## 取消宽带后路由器必须归还吗？
必须。宽带套餐赠送的路由器须在取消后30天内归还，不归还的，R3收取300元，R5 Pro收取600元。
## 怎么归还设备？
可以送到任意营业厅，也可以在星联宽带应用里预约免费上门取件。
## 设备损坏怎么办？
正常磨损没有问题。进水或拆机造成的损坏，按设备更换价收费。
`)

	nvPair("policies/installation-appointment-policy",
		`# Installation appointments
## Can I choose the installation time?
Yes. You can choose a morning slot from 9:00 to 12:00 or an afternoon slot from 13:00 to 18:00.
## What if I miss the appointment?
If nobody is home, the technician waits 15 minutes. After that the visit counts as missed, and the next one must be booked again.
## How do I change an appointment?
Change it in the NovaNet app at least 4 hours before the slot. A change is free up to 2 times.
## Do I need to be present?
An adult account member must be home to let the technician in and to sign the installation form.
`,
		`# 安装预约规则
## 可以选择安装时间吗？
可以。上午时段为9:00至12:00，下午时段为13:00至18:00。
## 错过预约怎么办？
家里没人时师傅会等待15分钟，之后视为爽约，需要重新预约。
## 怎么修改预约？
最晚在预约时段开始前4小时，在星联宽带应用里修改，免费修改最多2次。
## 安装时必须有人在家吗？
需要一位成年的账户成员在家开门，并签署安装单。
`)

	nvPair("policies/senior-care-eligibility",
		`# Senior Care eligibility
## Who can get Senior Care?
Senior Care is for customers aged 65 or older. The account holder is checked against the ID.
## Can I get Senior Care for my parents?
Yes, if your parent is the account holder. A child cannot be the holder of a Senior Care account.
## What if I need more data?
They can buy a Booster pack. Changing to another plan ends the Senior Care benefits.
`,
		`# 敬老套餐办理条件
## 谁可以办理敬老套餐？
敬老套餐面向年满65周岁的客户，办理时核验账户持有人的证件。
## 可以给父母办敬老套餐吗？
可以，但账户持有人必须是父母本人，子女不能作为敬老套餐的持有人。
## 敬老套餐客户想要更多流量怎么办？
可以购买加油包。改办其他套餐后，不再享受敬老套餐权益。
`)
}
