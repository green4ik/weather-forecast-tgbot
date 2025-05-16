package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
)

type WeatherResponse struct {
	Name string `json:"name"`
	Main struct {
		Temp     float64 `json:"temp"`
		Humidity int     `json:"humidity"`
	} `json:"main"`
	Weather []struct {
		Main        string `json:"main"`
		Description string `json:"description"`
	} `json:"weather"`
}
type ForecastResponse struct {
	List []struct {
		Dt   int64 `json:"dt"`
		Main struct {
			Temp float64 `json:"temp"`
		} `json:"main"`
		Weather []struct {
			Description string `json:"description"`
		} `json:"weather"`
	} `json:"list"`
}

var (
	userCities     = make(map[int64]string) // userID -> city
	mutex          sync.Mutex
	waitingForCity = make(map[int64]bool)
)

func getWeather(city string, weatherApiKey string) (string, error) {
	url := fmt.Sprintf("https://api.openweathermap.org/data/2.5/weather?q=%s&appid=%s&units=metric&lang=ua", city, weatherApiKey)

	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("помилка запиту: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var data WeatherResponse
	err = json.Unmarshal(body, &data)
	if err != nil || len(data.Weather) == 0 {
		return "", fmt.Errorf("не вдалося отримати дані для %s", city)
	}

	text := fmt.Sprintf("📍 Погода в місті %s:\n🌡 %.1f°C\n💧 Вологість: %d%%\n☁️ %s (%s)\n\n🧠 Коментар:\n",
		data.Name, data.Main.Temp, data.Main.Humidity, data.Weather[0].Description, data.Weather[0].Main)

	switch {
	case data.Main.Temp <= -10:
		text += "🥶 Надворі так холодно, що навіть Wi-Fi замерз!"
	case data.Main.Temp <= 0:
		text += "🧥 Вдягайся як капуста — шар за шаром."
	case data.Main.Temp <= 10:
		text += "🌀 Краще залишайся вдома з чаєм."
	case data.Main.Temp <= 20:
		text += "🌤 Легенький светрик не завадить."
	case data.Main.Temp <= 30:
		text += "😎 Ідеально! Йди ловити сонце."
	default:
		text += "🔥 Надворі жарко. Тримайся в тіні й пий воду."
	}

	switch data.Weather[0].Main {
	case "Rain":
		text += "\n☔ Парасоля — твій найкращий друг сьогодні."
	case "Snow":
		text += "\n❄ Головне — не лизати металеві предмети."
	case "Clear":
		text += "\n🌞 Можна засмагати, але не перегрівайся."
	case "Thunderstorm":
		text += "\n⛈ Краще не виходити з дому без причини."
	case "Clouds":
		text += "\n🌫 Ідеально для філософських думок про сенс життя."
	}

	return text, nil
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("❌ Не вдалося завантажити .env файл")
	}

	telegramToken := os.Getenv("TELEGRAM_TOKEN")
	weatherApiKey := os.Getenv("WEATHER_API_KEY")
	bot, err := tgbotapi.NewBotAPI(telegramToken)
	if err != nil {
		log.Panic(err)
	}
	bot.Debug = false
	log.Printf("✅ Бот запущено як %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		userID := update.Message.Chat.ID
		text := strings.TrimSpace(update.Message.Text)

		// Якщо чекаємо місто від юзера
		if waitingForCity[userID] {
			mutex.Lock()
			userCities[userID] = text
			waitingForCity[userID] = false
			mutex.Unlock()
			msg := tgbotapi.NewMessage(userID, fmt.Sprintf("✅ Місто \"%s\" збережено!", text))
			bot.Send(msg)
			continue
		}

		switch text {
		case "/start":
			keyboard := tgbotapi.NewReplyKeyboard(
				tgbotapi.NewKeyboardButtonRow(
					tgbotapi.NewKeyboardButton("📍 Задати місто"),
					tgbotapi.NewKeyboardButton("🌦 Показати погоду"),
				),
				tgbotapi.NewKeyboardButtonRow(
					tgbotapi.NewKeyboardButton("📅 Прогноз на день"),
				),
			)
			msg := tgbotapi.NewMessage(userID, "Привіт! Обери дію з меню:")
			msg.ReplyMarkup = keyboard
			bot.Send(msg)

		case "📍 Задати місто":
			waitingForCity[userID] = true
			bot.Send(tgbotapi.NewMessage(userID, "Введи назву міста:"))

		case "🌦 Показати погоду":
			mutex.Lock()
			city, exists := userCities[userID]
			mutex.Unlock()

			if !exists {
				bot.Send(tgbotapi.NewMessage(userID, "Спочатку задай місто через кнопку 📍 Задати місто."))
				continue
			}

			weatherText, err := getWeather(city, weatherApiKey)
			if err != nil {
				bot.Send(tgbotapi.NewMessage(userID, "Не вдалося отримати погоду. Спробуй ще раз."))
				continue
			}
			bot.Send(tgbotapi.NewMessage(userID, weatherText))
		case "📅 Прогноз на день":
			mutex.Lock()
			city, exists := userCities[userID]
			mutex.Unlock()

			if !exists {
				bot.Send(tgbotapi.NewMessage(userID, "Спочатку задай місто через кнопку 📍 Задати місто."))
				continue
			}

			forecastText, err := getDailyForecast(city, weatherApiKey)
			if err != nil {
				bot.Send(tgbotapi.NewMessage(userID, "Не вдалося отримати прогноз. Спробуй пізніше."))
				continue
			}
			bot.Send(tgbotapi.NewMessage(userID, forecastText))
		default:
			bot.Send(tgbotapi.NewMessage(userID, "Не впізнаю цю команду. Скористайся меню."))
		}
	}
}
func getDailyForecast(city string, weatherApiKey string) (string, error) {
	geoURL := fmt.Sprintf("http://api.openweathermap.org/geo/1.0/direct?q=%s&limit=1&appid=%s", city, weatherApiKey)
	resp, err := http.Get(geoURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var geoData []struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	}
	body, _ := io.ReadAll(resp.Body)
	json.Unmarshal(body, &geoData)
	if len(geoData) == 0 {
		return "", fmt.Errorf("не вдалося знайти місто")
	}

	lat := geoData[0].Lat
	lon := geoData[0].Lon

	forecastURL := fmt.Sprintf("https://api.openweathermap.org/data/2.5/forecast?lat=%f&lon=%f&appid=%s&units=metric&lang=ua", lat, lon, weatherApiKey)
	resp, err = http.Get(forecastURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ = io.ReadAll(resp.Body)
	var forecast ForecastResponse
	json.Unmarshal(body, &forecast)

	currentDate := time.Now().Format("2006-01-02")
	result := fmt.Sprintf("📅 Прогноз на день для %s:\n", city)

	for _, entry := range forecast.List {
		entryTime := time.Unix(entry.Dt, 0)
		if entryTime.Format("2006-01-02") == currentDate {
			desc := strings.ToLower(entry.Weather[0].Description)
			emoji := "🌡"

			switch {
			case strings.Contains(desc, "дощ"):
				emoji = "🌧"
			case strings.Contains(desc, "сніг"):
				emoji = "❄️"
			case strings.Contains(desc, "сонячно"):
				emoji = "☀️"
			case strings.Contains(desc, "ясно"):
				emoji = "🌞"
			case strings.Contains(desc, "гроза"):
				emoji = "⛈"
			case strings.Contains(desc, "хмар"):
				emoji = "☁️"
			case strings.Contains(desc, "туман"):
				emoji = "🌫"
			case strings.Contains(desc, "вітер"):
				emoji = "💨"
			}

			result += fmt.Sprintf("🕒 %s: %s %.1f°C — %s\n",
				entryTime.Format("15:04"),
				emoji,
				entry.Main.Temp,
				entry.Weather[0].Description)

		}
	}

	if result == "" {
		return "", fmt.Errorf("немає даних для поточного дня")
	}

	return result, nil
}
