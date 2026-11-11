# Comment editing guidelines

## User

Delete comments you deem unncesary, overly long or that has imperatives like "do this". Then humanize the rest. Brief, in turkish, human-like so no em dashes or semi-colon use

## Assistant

Yorumları tarayıp gereksiz, uzun veya emir kipindeki olanları çıkaracağım. Kalanları kısa ve doğal Türkçe hale getireceğim.

### Tool Glob File Search

```
{"globPattern":"**/*.{go,vue,ts,js}"}
```

### Tool Ripgrep Raw Search

```
{"pattern":"^\\s*//","path":"/Users/borakaraman/Desktop/insider_one_case","glob":"*.go","outputMode":"content","caseInsensitive":false,"headLimit":80,"multiline":false,"toolCallId":"call-727292d0-0c2d-4b1e-b3c3-5222dd027fa7-1\nfc_2c1cd34b-75aa-966d-aedf-18e5924d6451_1","offset":0}
```

### Tool Ripgrep Raw Search

```
{"pattern":"//","path":"/Users/borakaraman/Desktop/insider_one_case","glob":"*_test.go","outputMode":"content","caseInsensitive":false,"headLimit":30,"multiline":false,"toolCallId":"call-dae29e8d-701f-4f34-a278-756523c8b444-2\nfc_7b21bf4d-3711-921d-a9ff-c68e1014f7eb_0","offset":0}
```

### Tool Ripgrep Raw Search

```
{"pattern":"os\\.ReadFile|//go:embed","path":"/Users/borakaraman/Desktop/insider_one_case","glob":"*.go","outputMode":"content","caseInsensitive":false,"headLimit":40,"multiline":false,"toolCallId":"call-dae29e8d-701f-4f34-a278-756523c8b444-3\nfc_7b21bf4d-3711-921d-a9ff-c68e1014f7eb_1","offset":0}
```

### Tool Ripgrep Raw Search

```
{"pattern":"^\\s*//","path":"/Users/borakaraman/Desktop/insider_one_case","glob":"*.{vue,ts}","outputMode":"content","caseInsensitive":false,"headLimit":40,"multiline":false,"toolCallId":"call-dae29e8d-701f-4f34-a278-756523c8b444-4\nfc_7b21bf4d-3711-921d-a9ff-c68e1014f7eb_2","offset":0}
```

## Assistant

Yorumlar çoğu yerde uzun İngilizce açıklama. Kaynak dosyaları tarayıp sadece gerekli olanları kısa Türkçeye çevireceğim.

### Tool Ripgrep Raw Search

```
{"pattern":"^\\s*//","path":"/Users/borakaraman/Desktop/insider_one_case","glob":"*.{go,vue,ts}","outputMode":"count","caseInsensitive":false,"multiline":false,"toolCallId":"call-1b240111-57ce-4b76-8c3e-642b70419b4a-5\nfc_c1d7010a-a55e-9df9-843c-d7a4fd1f5afb_0","offset":0}
```

### Tool Read File V2

```
{"path":"/Users/borakaraman/Desktop/insider_one_case/cmd/app/main.go","limit":160}
```

### Tool Read File V2

```
{"path":"/Users/borakaraman/Desktop/insider_one_case/cmd/app/worker.go","limit":250}
```

### Tool Read File V2

```
{"path":"/Users/borakaraman/Desktop/insider_one_case/internal/store/migrate.go"}
```

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Run Terminal Command V2

### Tool Get Mcp Tools

```
{"server":"cursor","toolName":"Task","toolCallId":"call-695bce95-910c-4888-841c-587ec711f3c5-34\nfc_e99def7d-8b88-94bd-a9e1-2b6f42e21bec_0"}
```

### Tool New agent

### Tool New agent

### Tool New agent

### Tool New agent

## Assistant

Ajanların bıraktığı yorumları kontrol ediyorum. İngilizce kalan, emir kipi veya noktalı virgül varsa düzelteceğim.

### Tool Run Terminal Command V2

### Tool Run Terminal Command V2

### Tool Read File V2

```
{"path":"/Users/borakaraman/Desktop/insider_one_case/internal/delivery/limiter.go","limit":30}
```

### Tool Read File V2

```
{"path":"/Users/borakaraman/Desktop/insider_one_case/internal/store/migrate.go","limit":25}
```

### Tool Ripgrep Raw Search

```
{"pattern":"--","glob":"*.sql","outputMode":"content","caseInsensitive":false,"headLimit":30,"multiline":false,"toolCallId":"call-5ed6f5e9-7068-46e6-abfd-5e398aa25d72-43\nfc_bd89758c-ecb0-97ba-925c-1b2857b36788_2","offset":0}
```

### Tool Ripgrep Raw Search

```
{"pattern":"^\\s*#","glob":"*.{yml,yaml,sh,Dockerfile,mk}","outputMode":"content","caseInsensitive":false,"headLimit":20,"multiline":false,"toolCallId":"call-5ed6f5e9-7068-46e6-abfd-5e398aa25d72-44\nfc_bd89758c-ecb0-97ba-925c-1b2857b36788_3","offset":0}
```

### Tool Read File V2

```
{"path":"/Users/borakaraman/Desktop/insider_one_case/deploy/postgres-init/01-mockprovider.sql","limit":20}
```

### Tool Read File V2

```
{"path":"/Users/borakaraman/Desktop/insider_one_case/.github/workflows/ci.yml","limit":20}
```

### Tool Read File V2

```
{"path":"/Users/borakaraman/Desktop/insider_one_case/docker-compose.yml","limit":25}
```

### Tool Ripgrep Raw Search

```
{"pattern":"^#","glob":"Makefile","outputMode":"content","caseInsensitive":false,"multiline":false,"toolCallId":"call-6c6a7b4c-c21a-428c-974b-c1cd765bf09a-48\nfc_03c271a8-00ef-936c-b44c-0586ecfa3575_3","offset":0}
```

### Tool Ripgrep Raw Search

```
{"pattern":"ANTHROPIC_API_KEY|Rule 1|Stream 4|secret","path":"/Users/borakaraman/Desktop/insider_one_case/cmd/app","glob":"*ci*","outputMode":"content","caseInsensitive":false,"multiline":false,"toolCallId":"call-d815e400-041d-4e87-a40e-cdc43cecb059-49\nfc_c5abb510-5f6a-962a-8ab2-8bea22677c31_0","offset":0}
```

### Tool Read File V2

```
{"path":"/Users/borakaraman/Desktop/insider_one_case/.github/workflows/ci.yml","offset":100,"limit":140}
```

### Tool Read File V2

```
{"path":"/Users/borakaraman/Desktop/insider_one_case/Makefile","limit":80}
```

### Tool Ripgrep Raw Search

```
{"pattern":"^\\s*#","path":"/Users/borakaraman/Desktop/insider_one_case","glob":"*.{yml,yaml,sh,mk}","outputMode":"content","caseInsensitive":false,"multiline":false,"toolCallId":"call-8e401303-31d2-4851-af21-4b5b6918eacc-52\nfc_6b78dbfc-a547-9d1a-b97f-ba923c1b672d_1","offset":0}
```

### Tool Read File V2

```
{"path":"/Users/borakaraman/Desktop/insider_one_case/.github/workflows/ci.yml","offset":20,"limit":30}
```

### Tool Read File V2

```
{"path":"/Users/borakaraman/Desktop/insider_one_case/.github/dependabot.yml","limit":15}
```

### Tool Ripgrep Raw Search

```
{"pattern":"[^:]//[^/]","glob":"*.go","outputMode":"content","caseInsensitive":false,"headLimit":40,"multiline":false,"toolCallId":"call-64146b8d-1c25-40a5-87b7-d8b1c1bc07b6-55\nfc_f0cfbf48-0c39-9936-8099-e51915777fc0_0","offset":0}
```

### Tool Read File V2

```
{"path":"/Users/borakaraman/Desktop/insider_one_case/docker-compose.yml","offset":25,"limit":150}
```

### Tool Ripgrep Raw Search

```
{"pattern":"^#","path":"/Users/borakaraman/Desktop/insider_one_case","glob":"Dockerfile*","outputMode":"content","caseInsensitive":false,"multiline":false,"toolCallId":"call-3c21cb34-6fdc-4f13-a8b3-33000643967e-57\nfc_e91ae380-ec2a-9e93-8df3-3b96e26c3a28_0","offset":0}
```

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Read File V2

```
{"path":"/Users/borakaraman/Desktop/insider_one_case/Dockerfile","limit":15}
```

### Tool Read File V2

```
{"path":"/Users/borakaraman/Desktop/insider_one_case/web/Dockerfile","limit":8}
```

### Tool Read File V2

```
{"path":"/Users/borakaraman/Desktop/insider_one_case/.claude/hooks/guard-secrets.sh","limit":80}
```

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Ripgrep Raw Search

```
{"pattern":"throwaway","path":"/Users/borakaraman/Desktop/insider_one_case/.github/workflows/ci.yml","outputMode":"content","caseInsensitive":false,"multiline":false,"toolCallId":"call-0d00b60e-fef4-4098-ad1f-a1c933db2d33-105\nfc_d2572c93-5ea7-9bc3-85fc-995f06f449fb_0","offset":0}
```

### Tool Ripgrep Raw Search

```
{"pattern":"^#","path":"/Users/borakaraman/Desktop/insider_one_case/.githooks","outputMode":"content","caseInsensitive":false,"multiline":false,"toolCallId":"call-0d00b60e-fef4-4098-ad1f-a1c933db2d33-106\nfc_d2572c93-5ea7-9bc3-85fc-995f06f449fb_1","offset":0}
```

### Tool Ripgrep Raw Search

```
{"pattern":"^#","path":"/Users/borakaraman/Desktop/insider_one_case/.claude","outputMode":"content","caseInsensitive":false,"multiline":false,"toolCallId":"call-0d00b60e-fef4-4098-ad1f-a1c933db2d33-107\nfc_d2572c93-5ea7-9bc3-85fc-995f06f449fb_2","offset":0}
```

### Tool Read File V2

```
{"path":"/Users/borakaraman/Desktop/insider_one_case/.githooks/pre-commit_test.sh","limit":15}
```

### Tool Read File V2

```
{"path":"/Users/borakaraman/Desktop/insider_one_case/.claude/hooks/guard-secrets_test.sh","limit":20}
```

### Tool Edit File V2

### Tool Edit File V2

### Tool Read File V2

```
{"path":"/Users/borakaraman/Desktop/insider_one_case/.githooks/pre-commit","offset":10,"limit":40}
```

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Ripgrep Raw Search

```
{"pattern":"^#","path":"/Users/borakaraman/Desktop/insider_one_case/.githooks/pre-commit","outputMode":"content","caseInsensitive":false,"multiline":false,"toolCallId":"call-4db27d96-5ed3-4397-bd9c-efab7bdb196c-120\nfc_992b315c-a85e-9f18-abbb-984fcd7041ae_0","offset":0}
```

### Tool Ripgrep Raw Search

```
{"pattern":"^\\s*(#|//|--) .*[A-Za-z]{5,}","glob":"*.{conf,nginx,yml}","outputMode":"content","caseInsensitive":false,"headLimit":30,"multiline":false,"toolCallId":"call-4db27d96-5ed3-4397-bd9c-efab7bdb196c-121\nfc_992b315c-a85e-9f18-abbb-984fcd7041ae_1","offset":0}
```

### Tool Run Terminal Command V2

### Tool Read File V2

```
{"path":"/Users/borakaraman/Desktop/insider_one_case/web/nginx.conf","limit":50}
```

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Edit File V2

### Tool Ripgrep Raw Search

```
{"pattern":"proxy_buffering|Content-Security-Policy|nginx.conf","path":"/Users/borakaraman/Desktop/insider_one_case/web","glob":"*.ts","outputMode":"content","caseInsensitive":false,"headLimit":20,"multiline":false,"toolCallId":"call-27de221f-57cc-4241-afd5-ae0bf036cc6e-129\nfc_2d8defc4-8f79-9c92-8945-a80f01235268_0","offset":0}
```

## Assistant

Uzun, tekrarlayan ve emir kipindeki yorumları sildim. Kalanlar tek kısa Türkçe cümle. Noktalı virgül ve uzun çizgi yok.

Kod, log metinleri ve test iddiaları duruyor. `docs/` ve `DECISIONS.md` dosyalarına dokunmadım. `gofmt` temiz.

Şaşırtıcı yerlerde yorum duruyor. Örneğin rıza süzgecinin her zaman eklenmesi, anahtarın loglanmaması ve SSE akışının tamponlanmaması. Paket başlıkları da tek cümleye indi.

Önerilen commit mesajı:

```
Yorumları kısalt ve Türkçeleştir.

Uzun ve emredici notlar çıktı, şaşırtıcı davranışlar tek cümle kaldı.
```