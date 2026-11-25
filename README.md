# CLO Cloud Go

Общая библиотека интеграций CLO для Rancher Node Driver и Kubernetes CSI.
Это адаптер официального CLO SDK, с сохранёнными проверками полноты inventory,
редактированием ошибок, ограничением API-запросов и осторожной обработкой операций.

- `cloud`: API v2, VM, IPv4 и тома. Транспорт не повторяет неоднозначные изменения.
- `bootstrap`: композиция cloud-init и общий формат machine identity для Node Driver/CSI.

Go 1.27.1, официальный CLO SDK v3.2.0. Исходная реализация выделена без изменения
алгоритмов; все исходные unit/race tests перенесены. Пользователи закрепляют v0.1.1.

`cloud.New` ограничивает запросы одним в секунду и разделяет бюджет/паузы
`Retry-After` между клиентами одного процесса. HTTP 429 повторяется только для
GET, не более четырёх попыток в пределах контекста и HTTP timeout. Изменяющие
запросы возвращают первый результат владельцу операции без повтора.

Проверка: `make check`. Для bootstrap tests нужен Python3 + PyYAML из
`scripts/requirements-test.txt`. Тесты не создают облачные ресурсы.

Потребители: [Node Driver](https://github.com/RaveTechnology/docker-machine-driver-clo),
[CSI](https://github.com/RaveTechnology/clo-csi). Лицензия Apache-2.0.
