package strategy

import (
	. "common"
	. "config"
	"email"
	"fmt"
	"logger"
	"strconv"
	"time"
)

// Strategy is the interface that must be implemented by a strategy driver.
type Strategy interface {
	Tick(records []Record) bool
}

const timeout = 5 //minute

var strategys = make(map[string]Strategy)
var PrevTrade string
var PrevBuyPirce float64
var warning string

var buyOrders map[time.Time]string
var dealOrders map[time.Time]Order
var sellOrders map[time.Time]string
var recancelbuyOrders map[time.Time]string
var resellOrders map[time.Time]string

var buy_average float64
var buy_amount float64

var gTradeAPI TradeAPI

func init() {
	buyOrders = make(map[time.Time]string)
	dealOrders = make(map[time.Time]Order)
	sellOrders = make(map[time.Time]string)
	recancelbuyOrders = make(map[time.Time]string)
	resellOrders = make(map[time.Time]string)

	buy_average = 0

	PrevTrade = "init"
}

// Register makes a strategy available by the provided name.
// If Register is called twice with the same name or if driver is nil,
// it panics.
func Register(strategyName string, strategy Strategy) {
	if strategy == nil {
		panic("sql: Register strategy is nil")
	}
	if _, dup := strategys[strategyName]; dup {
		panic("sql: Register called twice for strategy " + strategyName)
	}
	strategys[strategyName] = strategy
}

//entry call
func Tick(tradeAPI TradeAPI, records []Record) bool {

	strategyName := Option["strategy"]
	strategy, ok := strategys[strategyName]
	if !ok {
		logger.Errorf("sql: unknown strategy %q (forgotten import? private strategy?)", strategyName)
		return false
	}

	if strategyName != "OPENORDER" {
		length := len(records)
		//
		if length == 0 {
			logger.Errorln("warning:detect exception data", len(records))
			return false
		}

		//check exception data in trade center
		if checkException(records[length-2], records[length-1]) == false {
			logger.Errorln("detect exception data of trade center",
				records[length-2].Close, records[length-1].Close, records[length-1].Volumn)
			return false
		}
	}

	gTradeAPI = tradeAPI
	return strategy.Tick(records)
}

//check exception data in trade center
func checkException(recordPrev, recordNow Record) bool {
	if recordNow.Close > recordPrev.Close+10 && recordNow.Volumn < 1 {
		return false
	}

	if recordNow.Close < recordPrev.Close-10 && recordNow.Volumn < 1 {
		return false
	}

	return true
}

func getTradePrice(tradeDirection string, price float64) string {
	slippage, err := strconv.ParseFloat(Option["slippage"], 64)
	if err != nil {
		logger.Debugln("config item slippage is not float")
		slippage = 0
	}

	var finalTradePrice float64
	if tradeDirection == "buy" {
		finalTradePrice = price * (1 + slippage*0.001)
	} else if tradeDirection == "sell" {
		finalTradePrice = price * (1 - slippage*0.001)
	} else {
		finalTradePrice = price
	}

	return fmt.Sprintf("%0.02f", finalTradePrice)
}

func getBuyPrice() (price string, nPrice float64, warning string) {
	//compute the price
	slippage, err := strconv.ParseFloat(Option["slippage"], 64)
	if err != nil {
		logger.Debugln("config item slippage is not float")
		slippage = 0.01
	}

	ret, orderBook := GetOrderBook()
	if !ret {
		logger.Infoln("get orderBook failed 1")
		ret, orderBook = GetOrderBook() //try again
		if !ret {
			logger.Infoln("get orderBook failed 2")
			return
		}
	}

	logger.Infoln("卖一", (orderBook.Asks[len(orderBook.Asks)-1]))
	logger.Infoln("买一", orderBook.Bids[0])
	nPrice = orderBook.Bids[0].Price + slippage
	price = fmt.Sprintf("%f", nPrice)
	warning += "---->限价单" + price

	return
}

func buy(price, amount string) string {
	nPrice, err := strconv.ParseFloat(price, 64)
	if err != nil {
		logger.Debugln("price is not float")
		return "0"
	}

	var buyID string
	if Option["enable_trading"] == "1" {
		buyID = gTradeAPI.Buy(price, amount)
	} else {
		buyID = "-1"
	}

	if buyID != "0" {
		buyOrders[time.Now()] = buyID
		PrevTrade = "buy"
		PrevBuyPirce = nPrice
	}

	return buyID
}

func sell(price, amount string) string {
	var sellID string
	if Option["enable_trading"] == "1" {
		sellID = gTradeAPI.Sell(price, amount)
	} else {
		sellID = "-1"
	}

	if sellID != "0" {
		sellOrders[time.Now()] = sellID
		PrevTrade = "sell"
		PrevBuyPirce = 0
	}

	return sellID
}

func Buy() string {
	if PrevTrade == "buy" {
		return "0"
	}

	//compute the price
	price, nPrice, warning := getBuyPrice()

	//compute the amount
	amount := Option["tradeAmount"]
	nAmount, err := strconv.ParseFloat(amount, 64)
	if err != nil {
		logger.Infoln("amount is not float")
	}

	Available_cny := GetAvailable_cny()
	if Available_cny < nPrice*nAmount {
		var nMinTradeAmount float64
		nAmount = Available_cny / nPrice
		symbol := Option["symbol"]
		if symbol == "btc_cny" {
			nMinTradeAmount = 0.1
		} else {
			nMinTradeAmount = 0.01
		}
		if nAmount < nMinTradeAmount {
			warning += "没有足够的法币可用"
			logger.Infoln(warning)
			return "0"
		}

		amount = fmt.Sprintf("%02f", nAmount)
	}

	warning += "---->数量" + amount

	buyID := buy(price, amount)
	if buyID == "-1" {
		warning += " [模拟]"
	} else if buyID == "0" {
		warning += "[委托失败]"
	} else {
		warning += "[委托成功]" + buyID
	}

	logger.Infoln(warning)
	go email.TriggerTrender(warning)

	return buyID
}

func getSellPrice() (price string, nPrice float64, warning string) {
	//compute the price
	slippage, err := strconv.ParseFloat(Option["slippage"], 64)
	if err != nil {
		logger.Debugln("config item slippage is not float")
		slippage = 0.01
	}

	ret, orderBook := GetOrderBook()
	if !ret {
		logger.Infoln("get orderBook failed 1")
		ret, orderBook = GetOrderBook() //try again
		if !ret {
			logger.Infoln("get orderBook failed 2")
			return
		}
	}

	logger.Infoln("卖一", (orderBook.Asks[len(orderBook.Asks)-1]))
	logger.Infoln("买一", orderBook.Bids[0])
	nPrice = orderBook.Asks[len(orderBook.Asks)-1].Price - slippage
	price = fmt.Sprintf("%f", nPrice)
	warning += "---->限价单" + price

	return
}

func Sell() string {
	if PrevTrade == "sell" {
		return "0"
	}

	//compute the price
	price, _, warning := getSellPrice()

	//compute the amount
	Available_coin := GetAvailable_coin()
	if Available_coin < 0.01 {
		warning = "the3crow down, but 没有足够的币可卖"
		logger.Infoln(warning)
		PrevTrade = "sell"
		PrevBuyPirce = 0
		return "0"
	}

	amount := Option["tradeAmount"]
	nAmount, err := strconv.ParseFloat(amount, 64)
	if err != nil {
		logger.Infoln("amount is not float")
		return "0"
	}

	if nAmount > Available_coin {
		nAmount = Available_coin
		amount = fmt.Sprintf("%02f", nAmount)
	}

	sellID := sell(price, amount)
	if sellID == "-1" {
		warning += " [模拟]"
	} else if sellID == "0" {
		warning += "[委托失败]"
	} else {
		warning += "[委托成功]" + sellID
	}

	logger.Infoln(warning)
	go email.TriggerTrender(warning)

	return sellID
}

func CancelOrder(order_id string) bool {
	return gTradeAPI.CancelOrder(order_id)
}
func GetAccount() (Account, bool) {
	return gTradeAPI.GetAccount()
}
func GetOrderBook() (ret bool, orderBook OrderBook) {
	return gTradeAPI.GetOrderBook()
}

func GetOrder(order_id string) (ret bool, order Order) {
	return gTradeAPI.GetOrder(order_id)
}

func GetAvailable_cny() float64 {
	account, ret := GetAccount()
	if !ret {
		logger.Errorln("GetAccount failed")
		return -1
	}

	numAvailable_cny, err := strconv.ParseFloat(account.Available_cny, 64)
	if err != nil {
		logger.Errorln("tradeAmount is not float")
		return -1
	}
	//balance > 0
	return numAvailable_cny
}

func GetAvailable_btc() float64 {
	account, ret := GetAccount()
	if !ret {
		logger.Errorln("GetAccount failed")
		return -1
	}

	numAvailable_btc, err := strconv.ParseFloat(account.Available_btc, 64)
	if err != nil {
		logger.Errorln("Available_btc is not float")
		return -1
	}
	//nCoins > 0
	return numAvailable_btc
}

func GetAvailable_ltc() float64 {
	account, ret := GetAccount()
	if !ret {
		logger.Errorln("GetAccount failed")
		return -1
	}

	numAvailable_ltc, err := strconv.ParseFloat(account.Available_ltc, 64)
	if err != nil {
		logger.Errorln("Available_ltc is not float")
		return -1
	}
	//nCoins > 0
	return numAvailable_ltc
}

func GetAvailable_coin() float64 {
	symbol := Option["symbol"]
	if symbol == "btc_cny" {
		return GetAvailable_btc()
	} else {
		return GetAvailable_ltc()
	}
}

//////////////////////////////////
//common stop loss function
//////////////////////////////////

func processStoploss(Price []float64) bool {
	length := len(Price)

	stoploss, err := strconv.ParseFloat(Option["stoploss"], 64)
	if err != nil {
		logger.Errorln("config item stoploss is not float")
		return false
	}

	//do sell when price is below stoploss point
	stoplossPrice := PrevBuyPirce * (1 - stoploss*0.01)
	if Price[length-1] <= stoplossPrice {
		warning := "stop loss, 卖出Sell Out---->"
		logger.Infoln(warning)

		Sell()
	}

	return true
}

func processTimeout() bool {
	//check timeout trade

	//last cancel failed, recancel
	for tm, id := range recancelbuyOrders {
		warning := fmt.Sprintf("<-----re-cancel %s-------------->", id)
		if CancelOrder(id) {
			warning += "[Cancel委托成功]"
			delete(recancelbuyOrders, tm)
		} else {
			warning += "[Cancel委托失败]"
		}

		logger.Infoln(warning)
		time.Sleep(1 * time.Second)
		time.Sleep(500 * time.Microsecond)
	}

	for tm, tradeAmount := range resellOrders {
		warning := fmt.Sprintf("<-----re-sell %f-------------->", tradeAmount)
		logger.Infoln(warning)
		sellID := Sell()
		if sellID != "0" {
			warning += "[委托成功]"
			delete(resellOrders, tm)
			sellOrders[time.Now()] = sellID //append or just update "set"
		} else {
			warning += "[委托失败]"
		}

		logger.Infoln(warning)
		time.Sleep(1 * time.Second)
		time.Sleep(500 * time.Microsecond)
	}

	now := time.Now()
	if len(buyOrders) != 0 {
		//todo-
		logger.Infoln("BuyId len", len(buyOrders))
		for tm, id := range buyOrders {
			ret, order := GetOrder(id)
			if ret == false {
				continue
			}
			if order.Amount == order.Deal_amount {
				buy_average = (buy_amount*buy_average + order.Deal_amount*order.Price) / (buy_amount + order.Deal_amount)
				logger.Infof("buy_average=%0.02f\n", buy_average)
				dealOrders[tm] = order
				buy_amount += order.Deal_amount
				delete(buyOrders, tm)
			} else {
				if int64(now.Sub(tm)/time.Minute) <= timeout {
					continue
				}

				if order.Deal_amount > 0.0001 { //部分成交的买卖单
					buy_average = (buy_amount*buy_average + order.Deal_amount*order.Price) / (buy_amount + order.Deal_amount)
					logger.Infof("part of buy_average=%0.02f\n", buy_average)
					dealOrders[tm] = order
					buy_amount += order.Deal_amount
				}

				warning := fmt.Sprintf("<-----buy Delegation timeout, cancel %s[deal:%f]-------------->", id, order.Deal_amount)
				logger.Infoln(order)
				if CancelOrder(id) {
					warning += "[Cancel委托成功]"
				} else {
					warning += "[Cancel委托失败]"
					recancelbuyOrders[time.Now()] = id
				}

				delete(buyOrders, tm)
				logger.Infoln(warning)
				time.Sleep(1 * time.Second)
				time.Sleep(500 * time.Microsecond)
			}
		}
	}

	if len(sellOrders) != 0 {
		//todo-
		logger.Infoln("SellId len", len(sellOrders))
		for tm, id := range sellOrders {
			if int64(now.Sub(tm)/time.Second) <= timeout {
				continue
			}

			ret, order := GetOrder(id)
			if ret == false {
				continue
			}

			if order.Amount == order.Deal_amount {
				delete(sellOrders, tm)
				buy_amount -= order.Deal_amount
			} else {
				if int64(now.Sub(tm)/time.Minute) <= timeout {
					continue
				}
				if order.Deal_amount < order.Amount {
					ret, orderBook := GetOrderBook()
					if !ret {
						logger.Infoln("get orderBook failed 1")
						ret, orderBook = GetOrderBook() //try again
						if !ret {
							logger.Infoln("get orderBook failed 2")
							return false
						}
					}

					warning := "<--------------sell Delegation timeout, cancel-------------->" + id
					if CancelOrder(id) {
						warning += "[Cancel委托成功]"

						delete(sellOrders, tm)
						//update to delete, start a new order for sell in below

						buy_amount -= order.Deal_amount
						sell_amount := order.Amount - order.Deal_amount

						logger.Infoln("卖一", (orderBook.Asks[len(orderBook.Asks)-1]))
						logger.Infoln("买一", orderBook.Bids[0])

						warning := "timeout, resell 卖出Sell Out---->限价单"
						tradePrice := fmt.Sprintf("%f", orderBook.Asks[len(orderBook.Asks)-1].Price-0.01)
						tradeAmount := fmt.Sprintf("%f", sell_amount)
						sellID := sell(tradePrice, tradeAmount)
						if sellID != "0" {
							warning += "[委托成功]"
							sellOrders[time.Now()] = sellID //append or just update "set"
						} else {
							warning += "[委托失败]"
							resellOrders[time.Now()] = tradeAmount
						}
						logger.Infoln(warning)
					} else {
						warning += "[Cancel委托失败]"
					}
					logger.Infoln(warning)
					time.Sleep(1 * time.Second)
					time.Sleep(500 * time.Microsecond)
				}
			}
		}
	}

	return true
}

//todo:need to think about edge issue carefully
//compute any period k-line base on 1 minte kline
func getKLine(records []Record, periods int) (recordsN []Record) {
	length := len(records)
	lengthN := length / periods

	// Loop through the entire array.
	for i := 0; i < periods*lengthN; i = i + periods {
		var recordN Record
		recordN.TimeStr = records[i].TimeStr
		recordN.Time = records[i].Time

		recordN.Open = records[i].Open
		recordN.Close = records[i+periods-1].Close

		var LowPrice []float64
		var HignPrice []float64
		for j := 0; j < periods; j++ {
			LowPrice = append(LowPrice, records[i+j].Low)
			HignPrice = append(HignPrice, records[i+j].High)
			recordN.Volumn += records[i+j].Volumn
		}
		recordN.Low = arrayLowest(LowPrice)
		recordN.High = arrayHighest(HignPrice)

		// add points to the array.
		recordsN = append(recordsN, recordN)
	}

	return recordsN
}
