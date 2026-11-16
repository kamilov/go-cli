# Справочник API

Import path: `github.com/kamilov/go-cli`. Имя пакета: `cli`.
Схема задаётся при регистрации команд. После возврата из `bind` её нельзя менять.

| Задача | API | Примечание |
| --- | --- | --- |
| Создать приложение | `cli.New[T](name, bind, run)` | Корень дерева; `T` обычно выводится из функций. |
| Добавить подкоманду | `cmd.Command[T](name, bind, run)` | Возвращает узел; `Run` вызывается на корне. |
| Узел без данных | `cli.NoArgs` | При `nil` для `bind` и `run` укажите `T` явно. |
| Ошибки схемы | `app.Err()` | Может содержать несколько `*cli.SchemaError`. |
| Запустить в Go-коде | `app.Run(ctx, args, opts...)` | `args` не включает имя программы. |
| Запустить из `main` | `app.Main()` | Печатает ошибку и возвращает exit code. |
| Получить метаданные | `cmd.Info()` | Снимок команды и поддерева. |
| Получить completion | `app.Complete(ctx, words, opts...)` | Последнее слово редактируется. |

## Методы Builder

| API | Назначение |
| --- | --- |
| `Describe(text)` | Описание команды в help. |
| `Alias(names...)` | Альтернативные имена подкоманды. |
| `Hidden()` | Скрыть команду из списков и completion. |
| `Deprecated(message)` | Пометить команду устаревшей; при запуске выводить warning. |
| `Version(value)` | Включить `--version` на корне. |
| `HelpFunc(fn)` | Заменить renderer справки на корне. |
| `Flag(&field, name, opts...)` | Одиночное значение, `bool` или `Count`. |
| `List(&slice, name, opts...)` | Повторяемый flag с элементами. |
| `Map(&map, name)` | Повторяемый flag с `key=value`. |
| `Arg(&field, name)` | Один positional arg. |
| `Args(&slice, name)` | Последний, variadic positional arg. |
| `Check(fn)` | Проверка структуры после проверки отдельных значений. |
| `Wrap(fn)` | Wrapper для команды и поддерева. |

Для `Flag`, `List` и `Map` доступны `Short`, `Env`, `Describe`, `Default`, `Parse`, `Required`, `Hidden`, `Deprecated`, `Check`, `Complete`.
Для `Arg` доступны `Describe`, `Default`, `Optional`, `Parse`, `Check`, `Complete`.
Для `Args` доступны `Describe`, `Optional`, `Parse`, `Check`, `Complete`.

## Типизированные опции и доступ из context

| API | Назначение |
| --- | --- |
| `cli.OneOf(values...)` | Допустимые значения для `Flag` или элементы `List`. |
| `cli.Count()` | Счетчик для `Flag[int]`. |
| `cli.Negatable()` | Форма `--no-name` для `Flag[bool]`. |
| `cli.FromString[T]`, `cli.FromInt[T]` | Готовые parsers для именованных строк и целых. |
| `cli.Parent[T](ctx)` | Ближайшая структура предка типа `T`. |
| `cli.SourceOf(ctx, &field)` | `SourceUnset`, `SourceDefault`, `SourceEnv` или `SourceCommandLine`. |
| `cli.Stdin(ctx)`, `cli.Stdout(ctx)`, `cli.Stderr(ctx)` | Потоки текущего вызова. |
| `cli.WithStdin`, `cli.WithStdout`, `cli.WithStderr`, `cli.WithEnv` | Параметры отдельного `Run` или `Complete`. |

`*cli.InputError` содержит путь команды в `Path`. `*cli.SchemaError` содержит позицию объявления в `Pos`.
Порядок обработки описан в [разделе о запуске](runtime.md).
