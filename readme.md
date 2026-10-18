# jetkvm-status-agent

JetKVM의 MQTT 상태를 수집하고 WebRTC 접속 없이 인증된 HTTP 조회 API로 제공하는 Go 기반 에이전트입니다.

**현재 단계: v0.1.0 실행 가능한 초안. Windows 모의 실행과 Linux ARM 교차 빌드 확인, JetKVM 실장비 설치·수신 미검증.**

## 구현 범위

- 별도 MQTT 3.1.1 브로커 구독: ATX 전원 LED, DC 출력, 온라인 알림, 원격 세션 수
- 장비별 정확한 토픽 4개만 구독, MQTT 연결 실패 시 자동 재시도·재접속
- 인증된 상태 API와 프로세스 응답 확인용 health API
- 미수신 `null`, 보관 메시지 `unknown`, 유효시간 경과·연결 단절 `stale` 구분
- 재시작·브로커 재접속 시 이전 관측값 초기화, 상태는 메모리에만 보관
- Bearer 토큰, 외부 인터페이스 HTTPS 강제, 원격 브로커 TLS 검증
- Windows AMD64, Linux ARMv7·ARM64 빌드 스크립트 및 자동 검사 workflow

초안은 **조회 전용**입니다. 전원 명령, 영상·입력 전달, 확장 종류 확정, 기존 PC 수신기로 중계, 자동 설치·업데이트·제거·부팅 서비스 등록은 구현하지 않았습니다. 펌웨어·시리얼·GPIO·시스템 설정을 변경하지 않습니다. UI가 없어 로고 자산은 아직 포함하지 않습니다. UI 추가 시 NexaCore 공통 브랜드를 프로젝트 내부 자산으로 적용합니다.

## 구조와 준비 조건

```text
JetKVM MQTT 발행 → 별도 로컬 브로커 → status-agent → 인증된 조회 API → Android 등
```

브로커를 에이전트에 내장하지 않고 Eclipse Paho 클라이언트로 구독합니다. 브로커가 별도로 필요하며 JetKVM의 MQTT 목적지가 그 브로커를 가리켜야 합니다. 기본 주소는 loopback이며 장비 설치 전에는 PC의 모의 브로커로 시험할 수 있습니다.

`configs/mosquitto.example.conf`는 Mosquitto 2.x용 loopback 예시입니다. 무인증 입력은 해당 장비의 로컬 프로세스에만 허용합니다. 이 설정의 실장비 패키지 호환성·메모리 사용량은 미검증입니다. MQTT 패킷의 메모리 할당을 제한하려면 브로커의 `max_packet_size`도 적용해야 합니다. 애플리케이션은 수신 후 16 KiB 초과 payload를 무시합니다.

**기존 `jetkvm-monitor-switcher` 전용 수신기는 일반 구독 브로커가 아닙니다.** 현재 에이전트로 MQTT 목적지를 바꾸면 기존 PC 감지가 중단될 수 있습니다. 기존 설정을 백업하고 중계가 검증되기 전까지 운영 장비의 목적지를 전환하지 마세요. 이번 작업에서는 장비 설정을 변경하지 않았습니다.

## 빌드와 로컬 실행

Go 1.24 이상과 의존성 다운로드용 네트워크가 필요합니다. 검증 시 사용한 Go는 1.27.2입니다. Python 3는 모의 시험에만 필요합니다.

```powershell
go test ./...
go vet ./...
./scripts/build.ps1
Copy-Item configs/config.example.json config.json
```

`config.json`의 `topicPrefix`를 **실제 발행 토픽의 장비 ID까지 포함한 접두사**로 수정합니다. 예: `jetkvm/DEVICE_ID`. 펌웨어는 설정 접두사에 장비 ID를 추가할 수 있으므로 실제 토픽을 확인해야 합니다. `config.json`과 빌드 산출물은 Git에서 제외합니다.

```powershell
# 32바이트 임의 토큰. 실제 값은 터미널에 출력하거나 저장소에 넣지 않습니다.
$env:JETKVM_AGENT_TOKEN = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
# 브로커 인증을 사용하는 경우에만 지정
# $env:JETKVM_MQTT_USERNAME = '...'
# $env:JETKVM_MQTT_PASSWORD = '...'
./dist/jetkvm-status-agent-windows-amd64.exe -config config.json
```

같은 환경변수를 가진 다른 클라이언트에서 조회합니다. `/healthz`만 무인증이며 장비 정보를 포함하지 않습니다.

```powershell
Invoke-RestMethod http://127.0.0.1:9080/healthz
Invoke-RestMethod http://127.0.0.1:9080/api/v1/status -Headers @{Authorization="Bearer $env:JETKVM_AGENT_TOKEN"}
```

Linux 수동 빌드·실행:

```sh
go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags='-s -w' -o dist/jetkvm-status-agent-linux-arm ./cmd/jetkvm-status-agent
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o dist/jetkvm-status-agent-linux-arm64 ./cmd/jetkvm-status-agent
# 대상 장비의 uname -m 및 실행 환경을 확인한 뒤 알맞은 파일을 선택합니다.
chmod +x ./jetkvm-status-agent-linux-arm
# JETKVM_AGENT_TOKEN은 배포 환경의 비밀 변수로 주입합니다.
./jetkvm-status-agent-linux-arm -config config.json
```

ARM 교차 빌드는 장비 실행 성공을 의미하지 않습니다. 실제 배포 전 CPU·메모리·저장 공간·포트와 부팅 방식을 확인해야 합니다. 설치·자동 시작은 아직 제공하지 않습니다. Ctrl+C/SIGTERM으로 HTTP 종료를 기다리고 MQTT를 해제합니다.

## 설정

| 키 | 의미 |
|---|---|
| `httpListen` | 기본 `127.0.0.1:9080`. 외부 바인딩은 인증서·키 필수 |
| `mqttBroker` | `tcp://127.0.0.1:1883` 또는 `ssl://HOST:PORT`. 평문은 숫자 loopback IP만 허용 |
| `topicPrefix` | 와일드카드 없는 장비별 정확한 접두사 |
| `staleAfterSeconds` | 1~86400초, 예시 45초. 장비 측정으로 확정한 값이 아님 |
| `tlsCert`, `tlsKey` | HTTP 서버 PEM 인증서·키 경로. TLS MQTT는 시스템 신뢰 루트 사용 |

토큰은 `JETKVM_AGENT_TOKEN`으로만 받고 최소 32바이트를 요구합니다. 브로커 비밀 정보는 `JETKVM_MQTT_USERNAME`, `JETKVM_MQTT_PASSWORD`로 받습니다. 프로그램은 환경변수·payload·토큰을 로그에 출력하지 않습니다. 인증서는 Android 등 호출자가 신뢰해야 합니다. SSH 터널 또는 TLS 역방향 프록시를 사용할 때는 에이전트를 loopback에 유지할 수 있습니다.

초안은 로컬·신뢰된 브로커 환경을 대상으로 합니다. 별도 요청 속도 제한과 연결 수 제한은 아직 없으며, 인터넷 직접 공개 배포는 검증하지 않았습니다. HTTP에는 읽기·쓰기 시간 제한, 헤더 크기 제한과 상태 응답 `Cache-Control: no-store`를 적용합니다. 반복 상태 기록이나 주기적 로그 출력은 없습니다.

## API 계약 v1

| 메서드 | 경로 | 결과 |
|---|---|---|
| GET | `/healthz` | `{"ok":true}`. 프로세스 응답만 확인 |
| GET | `/api/v1/status` | 인증 성공 200, 누락·불일치 401 |

GET에 대응하는 HEAD도 허용하며, 다른 메서드는 405, 없는 경로는 404입니다. 브로커가 끊겨도 상태 API 자체는 200을 반환하며 `agent`와 각 관측값의 상태로 수집 실패를 전달합니다. **health 또는 HTTP 200을 장비 온라인으로 해석하면 안 됩니다.**

```json
{
  "schemaVersion": 1,
  "agent": {"version":"0.1.0","collectorConnected":true,"lastError":""},
  "device": {
    "online": {"value":true,"receivedAt":"2026-10-18T12:00:00Z","sourceObservedAt":null,"retained":false,"freshness":"fresh"},
    "extension":"unknown",
    "extensionVerified":false
  },
  "power": {
    "atx": {"value":true,"receivedAt":"2026-10-18T12:00:00Z","sourceObservedAt":null,"retained":true,"freshness":"unknown"},
    "dc": {"value":null,"receivedAt":null,"sourceObservedAt":null,"retained":false,"freshness":"unknown"},
    "usableForControl":false
  },
  "sessions": {"value":0,"receivedAt":"2026-10-18T12:00:00Z","sourceObservedAt":null,"retained":false,"freshness":"fresh"}
}
```

위 시각과 값은 예시입니다. 각 관측값의 원칙:

- `/status`의 `online`, `/atx/state`의 `power`, `/dc/state`의 `isOn`, `/sessions/state`의 `active_sessions`를 해석합니다.
- `value=null`은 미확인입니다. 미수신을 `false`나 `0`으로 바꾸지 않습니다.
- `power.atx.value`는 ATX LED 입력, `power.dc.value`는 DC 출력입니다. OS 부팅 완료를 의미하지 않습니다.
- `receivedAt`은 수신 시각이며 측정 시각이 아닙니다. `sourceObservedAt`은 항상 `null`입니다.
- `fresh`는 비보관 메시지를 연결 중 설정 시간 내 수신했다는 뜻입니다. 브로커의 지연이나 순서 역전 여부를 증명할 수 없습니다.
- 보관 메시지는 `unknown`을 유지합니다. 비보관 값은 기간 경과 또는 연결 단절로 `stale`이 됩니다. 과거 값 자체는 표시용으로 남깁니다.
- 확장 종류는 항상 `unknown`, `extensionVerified=false`, `usableForControl=false`입니다. ATX/DC가 모두 도착해도 현재 확장을 추정하지 않습니다.
- 누락·타입 불일치·음수 접속 수·잘못된 JSON은 기존 값을 변경하지 않고 `lastError=invalid_payload`로 표시합니다. 다음 정상 수신 시 지웁니다.

`lastError`는 `mqtt_connecting`, `mqtt_disconnected`, `subscribe_failed`, `subscribe_denied`, `invalid_payload`, `payload_too_large` 또는 빈 문자열입니다. 초기 연결 불가 중에는 `mqtt_connecting`이 유지됩니다. 구독 거부 시 ACL을 수정하고 에이전트를 재시작하세요. 구독 실패 후 자동 재구독은 네트워크 재접속 시 수행합니다.

## 검증

```powershell
go test ./...
go vet ./...
python scripts/smoke.py dist/jetkvm-status-agent-windows-amd64.exe
```

2026-10-18 로컬 Windows 검증 결과:

| 항목 | 결과 |
|---|---|
| Go 단위 시험·vet | 통과 |
| 실행 파일 → 모의 MQTT → 인증 HTTP | 통과 |
| ATX 보관 값 unknown, DC false, 접속 수 | 통과 |
| TTL stale, 재접속 시 이전 값 초기화 | 통과 |
| API 인증 거부·health 응답 | 통과 |
| Windows AMD64 / Linux ARMv7 / ARM64 빌드 | 통과 |
| JetKVM 실행·실제 MQTT·CPU/메모리·재부팅 | 미검증 |
| Android 실기기 연동·기존 PC 중계 | 미검증·중계 미구현 |

모의 시험은 임시 포트의 MQTT fixture와 실행 파일을 사용합니다. 설정은 Git 제외 `.local/`에 생성하고 종료 시 프로세스를 중단합니다. CI는 Linux 단위 시험·race·vet·같은 모의 시험과 ARMv7 빌드를 수행합니다. CI 실행 결과는 로컬 검증과 별도로 확인해야 합니다.

## 다음 단계

1. 대상 JetKVM의 펌웨어·CPU·사용 가능 자원·부팅 관리 방식 확인
2. SSH 공개 키 인증으로 수동 시험 배치, MQTT 발행 주기·보관·재접속 실측
3. 기존 MQTT 설정 백업·복원과 PC 수신기로의 루프 방지 중계
4. 설치·업데이트 실패 롤백, 감시·자동 시작·제거 스크립트
5. Android 앱의 독립적인 장비 상태 표시·중단·복귀 시험

## 관련 프로젝트와 근거

- `jetkvm-android-control`: 장비 목록, 상태 표시, 기존 영상·입력·전원 조작 경로
- `jetkvm-monitor-switcher`: Windows 모니터 구성 전환과 접속 감지
- [JetKVM 개발·SSH](https://jetkvm.com/docs/advanced-usage/developing)
- [MQTT 연결·초기 발행](https://github.com/jetkvm/kvm/blob/8f6c3001b1064eb32b3f66afd04efbe536bbaf87/mqtt.go)
- [상태 발행](https://github.com/jetkvm/kvm/blob/8f6c3001b1064eb32b3f66afd04efbe536bbaf87/mqtt_publish.go)
- [ATX/DC 타입과 확장 설정](https://github.com/jetkvm/kvm/blob/8f6c3001b1064eb32b3f66afd04efbe536bbaf87/jsonrpc.go)

위 고정 커밋의 공식 소스로 토픽과 타입을 확인했습니다. 에이전트 자체 구현이며 실제 장비 펌웨어와의 차이는 추가 확인이 필요합니다. 기존 설계의 단일 `power.on`을 ATX/DC를 혼동하지 않는 관측별 형식으로 변경했습니다. 초기 API이므로 Android 쪽은 이 구현된 계약에 맞춰 연동해야 합니다. 라이선스는 미지정입니다.
