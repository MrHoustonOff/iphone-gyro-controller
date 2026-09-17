[English](../README.md) | **Русский**

# GyroBridge

GyroBridge превращает ваш смартфон (iOS или Android) в высокоточный гироскопический контроллер для ПК-игр и эмуляторов с низкой задержкой по протоколу Cemuhook DSU.

![Главный интерфейс GyroBridge](imgs/core%20screen.jpg)

Совместимо с Cemu, RPCS3, Ryujinx, Yuzu, Dolphin, PCSX2 и любыми играми и эмуляторами, поддерживающими протокол Cemuhook DSU.

---

## Быстрый старт

### 1. Скачивание
Скачайте `GyroBridge.exe` (или `GyroBridge-windows-arm64.exe` для Windows-устройств на процессорах ARM) со страницы [GitHub Releases](https://github.com/MrHoustonOff/iphone-gyro-controller/releases).

### 2. Запуск
Убедитесь, что ваш ПК и смартфон подключены к **одной локальной сети Wi-Fi**. Запустите `GyroBridge.exe`.

### 3. Подключение смартфона
- **iOS (iPhone / iPad)**: Нажмите **Начальная настройка** в приложении и выполните пошаговую установку локального профиля сертификата, необходимого Safari для доступа к гироскопу через HTTPS.
- **Android**: Отсканируйте QR-код на главном экране камерой и откройте страницу контроллера в Google Chrome.

### 4. Калибровка
После подключения положите телефон неподвижно на стол в игровом хвате и нажмите **Калибровать**. Калибровка занимает ~5 секунд и выполняется всего один раз для устройства.

### 5. Настройка эмулятора
В настройках управления вашего эмулятора укажите параметры сервера движения:
- **IP сервера**: `127.0.0.1`
- **Порт сервера**: `26760`
- **Протокол**: Cemuhook DSU

---

## 3D Телеметрия и диагностика

GyroBridge включает выделенное окно трехмерной телеметрии реального времени для проверки отзывчивости датчиков, ориентации и частоты пакетов DSU:

![3D телеметрия и диагностика](imgs/3d%20view%20screen.jpg)

---

## Сборка из исходников

Требования:
- [Go](https://go.dev/) 1.21+
- [Node.js](https://nodejs.org/) 18+
- [Wails CLI v2](https://wails.io) (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)

```bash
# Клонирование репозитория
git clone https://github.com/MrHoustonOff/iphone-gyro-controller.git
cd iphone-gyro-controller/gui

# Сборка под Windows x86_64
wails build -o GyroBridge.exe

# Сборка под Windows ARM64
wails build -platform windows/arm64 -o GyroBridge-arm64.exe
```

Скомпилированные файлы появятся в директории `gui/build/bin/`.

---

## Лицензия

Проект распространяется с открытым исходным кодом под лицензией [MIT License](../LICENSE).
