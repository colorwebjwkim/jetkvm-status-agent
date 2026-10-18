# jetkvm-status-agent

JetKVM의 MQTT 상태를 수집하고 WebRTC 접속 없이 인증된 HTTP 조회 API로 제공하는 Go 기반 에이전트입니다.

**현재 단계: v0.2.0 MQTT 중계 구현. Windows 모의 시험, Linux ARM 교차 빌드 및 JetKVM ARMv7 실장비 설치·상태 수신·기존 PC 중계 연결 확인. 물리 재부팅은 미검증.**

## 구현 범위

- 내장 loopback MQTT 수신 또는 별도 MQTT 3.1.1 브로커 구독: ATX 전원 LED, DC 출력, 온라인 알림, 원격 세션 수
- 장비별 정확한 토픽 4개만 수집, 외부 브로커 연결 실패 시 자동 재시도·재접속
- 기존 PC 수신기로 세션·온라인 상태 단방향 MQTT 중계, 만료·연결 단절 시 offline 전달
- 인증된 상태 API와 프로세스 응답 확인용 health API
- 미수신 `null`, 보관 메시지 `unknown`, 유효시간 경과·연결 단절 `stale` 구분
- 재시작·브로커 재접속 시 이전 관측값 초기화, 상태는 메모리에만 보관
- Bearer 토큰, 외부 인터페이스 HTTPS 강제, 원격 브로커 TLS 검증
- Windows AMD64, Linux ARMv7·ARM64 빌드 스크립트 및 자동 검사 workflow

초안은 **조회 전용**입니다. 전원 명령, 영상·입력 전달, 확장 종류 확정, 범용 자동 설치·업데이트·제거는 구현하지 않았습니다. 실제 확인한 BusyBox 부팅 경로용 서비스 스크립트를 제공하며 시험 장비에 등록했습니다. 펌웨어 실행 파일·시리얼·GPIO는 변경하지 않습니다. 배치 시 기존 MQTT 설정을 백업하고 목적지를 loopback 수신부로 변경합니다. UI가 없어 로고 자산은 아직 포함하지 않습니다. UI 추가 시 NexaCore 공통 브랜드를 프로젝트 내부 자산으로 적용합니다.

## 구조와 준비 조건

```text
JetKVM MQTT 발행 → status-agent 내장 loopback 수신 → 인증된 조회 API
                                              └→ 기존 PC MQTT 수신기
```

외부 브로커 구독 모드와 Mochi MQTT 기반 내장 수신 모드를 제공합니다. 내장 수신 모드는 별도 브로커 설치 없이 JetKVM 로컬 발행을 수신하고 API 및 PC 중계에 사용합니다. 외부 구독 모드는 별도 브로커가 필요합니다. 기본 주소는 loopback이며 장비 설치 전에는 PC의 모의 브로커로 시험할 수 있습니다.

`configs/mosquitto.example.conf`는 Mosquitto 2.x용 loopback 예시입니다. 무인증 입력은 해당 장비의 로컬 프로세스에만 허용합니다. 이 설정의 실장비 패키지 호환성·메모리 사용량은 미검증입니다. MQTT 패킷의 메모리 할당을 제한하려면 브로커의 `max_packet_size`도 적용해야 합니다. 애플리케이션은 수신 후 16 KiB 초과 payload를 무시합니다.

**기존 `jetkvm-monitor-switcher` 전용 수신기는 일반 구독 브로커가 아닙니다.** 내장 수신 모드와 PC 중계를 함께 활성화하면 장비에서 PC로 발행하는 경로를 유지할 수 있습니다. 전환 전 기존 MQTT 설정을 백업하고 PC에서 실제 수신되는지 확인해야 합니다. 시험 장비에서 설정을 백업한 뒤 MQTT broker 주소만 loopback으로 전환했고, 기존 PC 수신기와 중계 연결을 확인했습니다.

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

ARM 교차 빌드는 장비 실행 성공을 의미하지 않습니다. 실제 배포 전 CPU·메모리·저장 공간·포트와 부팅 방식을 확인해야 합니다. 범용 설치기는 없으며, 시험 장비에는 아래 운영 절차로 수동 배치와 자동 시작 등록을 완료했습니다. Ctrl+C/SIGTERM으로 HTTP 종료를 기다리고 MQTT를 해제합니다.

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
  "agent": {"version":"0.2.0","collectorConnected":true,"lastError":""},
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
- 누락·타입 불일치·음수 접속 수·잘못된 JSON은 기존 표시 값을 보존하고 해당 관측값을 `unknown`, `lastError=invalid_payload`로 표시합니다. 세션·온라인 관측에 오류가 있으면 중계를 offline 처리하며 해당 토픽이 정상 갱신돼야 다시 전달합니다. 오류 문구는 다음 정상 수신 시 지웁니다.

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
| JetKVM ARMv7 실행·실제 MQTT | online, sessions, 최초 ATX power=true 수신 통과 |
| 장비 자원 짧은 표본 | RSS 약 9~10.2 MiB, 7 threads, 단일 CPU 약 0.4%/5초 표본 |
| 에이전트 강제 종료 | 약 3초 후 재실행, 이전 값 unknown 초기화·장비 재접속 통과 |
| 부팅 서비스·물리 재부팅 | 실제 init 경로 등록·서비스 시작/정지 확인, 물리 재부팅 미수행 |
| 내장 수신 → 구독 거부형 PC 모의 수신기 | QoS 1 세션·offline 중계 통과 |
| 잘못된 장비·무관 토픽·추가 펌웨어 telemetry | 잘못된 장비 거부, 무관 관측 폐기·추가 발행 ACK 통과 |
| 기존 PC 실장비 중계 | 장비 출발지 검증 시험 수신기 3개 세션 메시지 수신, 실제 PC 앱 연결 확인 |
| 기존 PC 감지 정책 | Android 영상 감지 기준·포트·선택 모니터 유지, MQTT 수신 포트 재시작 확인 |
| Android HTTPS API 연동·물리 모니터 전환 | 이번 변경 미검증 |

모의 시험은 임시 포트의 MQTT fixture와 실행 파일을 사용합니다. 설정은 Git 제외 `.local/`에 생성하고 종료 시 프로세스를 중단합니다. CI는 Linux 단위 시험·race·vet·같은 모의 시험과 ARMv7 빌드를 수행합니다. CI 실행 결과는 로컬 검증과 별도로 확인해야 합니다.

## 내장 수신 및 PC 중계

`configs/embedded.example.json`을 `config.json`으로 복사하고 실제 장비의 ID·토픽 접두사와 PC 주소로 바꿉니다. `mqttListen`이 있으면 내장 수신 모드이며 `mqttBroker` 대신 이 포트에서 직접 수신합니다.

| 설정 | 의미 |
|---|---|
| `mqttListen` | 숫자 loopback 주소만 허용, 예: `127.0.0.1:1883` |
| `deviceClientId` | 펌웨어 발행자의 실제 MQTT client ID, 보통 `jetkvm-장비ID` |
| `relayBroker` | 기존 PC 수신기의 `tcp://PC주소:1883` 또는 TLS 브로커 주소 |
| `relayAllowPlaintext` | 기존 평문 PC 수신기와 호환할 때만 `true`. 신뢰하는 LAN/Tailscale 전제 |

중계는 내장 수신 모드에서만 활성화할 수 있습니다. 외부 브로커 구독 모드의 중계는 동일 브로커의 별칭·브리지로 되돌아오는 루프를 막기 위해 설정 검사에서 거부합니다. 중계 연결은 내장 수신부가 허용하는 장비 client ID와 달라 입력으로 재유입될 수도 없습니다.

중계 비밀 정보는 필요할 때만 `JETKVM_RELAY_USERNAME`, `JETKVM_RELAY_PASSWORD`로 전달합니다. PC 수신기는 원래 JetKVM IP에서 오는 연결을 허용하므로 에이전트는 **JetKVM 안에서** 실행해야 합니다. 다른 PC에서 실행해도 동일한 출발지 IP가 되지 않습니다.

- 중계 목적지는 구독하지 않습니다. `/status`와 `/sessions/state`만 원래 장비 토픽으로 QoS 1, retain=false 발행합니다. ATX/DC와 명령은 중계하지 않습니다.
- 온라인 보고와 신선한 비보관 접속 수가 모두 확인돼야 접속 수를 전달합니다. 0명은 실제 수신한 0명에만 대응합니다.
- 원본 단절·잘못된 관측·TTL 만료 시 `online=false`로 PC의 상태를 미확인으로 돌립니다. PC의 기존 모드 유지 정책과 연결 단절 처리를 사용합니다.
- 목적지 단절 중의 큐·디스크 저장·보관 메시지 재생은 없습니다. 재접속 시 현재도 유효한 관측만 확인해 전송합니다. 전송 실패한 연결은 폐기하고 5초 간격으로 새 연결을 시도합니다.
- 프로세스 비정상 종료는 MQTT Will `online=false` 및 PC의 연결 단절로 처리합니다. 정상 종료도 offline을 전송합니다.
- 내장 수신부는 지정한 client ID만 받아들이며 모든 구독을 거부합니다. 펌웨어의 추가 metrics/discovery 발행은 QoS 1 ACK 후 폐기합니다. MQTT 3.1.1에서 이를 거부하면 장비가 계속 재접속할 수 있어 수신만 허용하고 수집하지 않습니다.
- 내장 수신부는 retained 저장을 하지 않습니다. 발행자가 보낸 PUBLISH의 RETAIN 비트는 저장 요청이므로 직접 받은 값은 실시간 관측입니다. 외부 브로커에서 전달된 retained 메시지는 여전히 `unknown`입니다.
- MQTT 패킷 최대 32 KiB, 연결 최대 2개, inflight 최대 16개를 적용합니다. HTTP와 MQTT 상태는 메모리에 유지합니다.
- `relay.enabled`, `connected`, `lastError`, `lastForwardedAt`, `forwarded`를 상태 API에 추가했습니다. 기존 schemaVersion 1 필드는 유지합니다. `connected`는 목적지 MQTT 연결이며 PC 모니터가 실제 전환됐다는 뜻은 아닙니다.

검증한 PC는 Android 영상 접속 알림을 모니터 감지 기준으로 사용하며 MQTT 수신기도 함께 실행합니다. 에이전트 배치 과정에서 이 감지 기준을 변경하지 않습니다. 이 모드에서 MQTT 중계 수신만으로 실제 모니터 전환을 증명할 수 없으므로 수신 증거와 전환 증거를 구분해야 합니다.

실장비 배치 순서: SSH 공개 키 인증 → CPU/메모리/기존 포트 확인 → 기존 설정 백업 → 임시 실행 및 API 확인 → MQTT 목적지만 loopback 수신부로 전환 → 장비 수집과 PC 중계 확인. 실패하면 기존 MQTT 목적지를 복원하고 에이전트를 중단합니다. 시험 장비에서는 실제 부팅 스크립트의 `/userdata/init.d/S??*` 실행을 확인한 후 아래 서비스 파일을 등록했습니다.

## 실장비 배치 및 운영 기록 (2026-10-18)

- 대상: ARMv7 / BusyBox / Dropbear, 총 메모리 약 199 MiB. `/userdata`에 설치하고 loopback의 MQTT 1883·HTTP 9080 사용 가능 여부를 먼저 확인했습니다.
- 설치 경로: `/userdata/jetkvm-status-agent/jetkvm-status-agent`, `config.json`, `agent.env`, `service.sh`.
- 부팅 진입점: `scripts/S23jetkvm-status-agent`를 `/userdata/init.d/S23jetkvm-status-agent`에 배치했습니다. 장비의 기존 `RkLunch.sh`가 이 경로를 실행하는 것을 확인했습니다. 물리 재부팅은 하지 않았습니다.
- `agent.env`에는 설치별 토큰, `GOMEMLIMIT=24MiB`, `GOMAXPROCS=1`을 저장했습니다. 디렉터리와 실행 파일은 700, 설정·비밀 파일과 백업은 600입니다. **환경 파일과 shell script는 LF 줄바꿈이어야 합니다.**
- 원래 `/userdata/kvm_config.json`은 설치 디렉터리의 `kvm_config.before.json`으로 백업했습니다. 다른 필드와 펌웨어 실행 파일은 유지하고 `mqtt_config.broker`만 `127.0.0.1`로 변경했습니다. 설정 적용을 위해 기존 펌웨어 앱을 재시작했습니다.
- 에이전트 실행 파일 SHA-256: `2d4dd6d667d5532f60c8ce0331f1bfab346e76d84ab7d0d3b388d4a277b1cebb`. 로컬 ARMv7 최종 빌드와 장비 파일이 일치합니다.
- PC 수신 포트가 열리지 않은 상태를 확인해 게시된 기존 PC 앱만 같은 설정으로 재시작했습니다. 기존 PC 소스의 로컬 수정 사항은 빌드·커밋하지 않았습니다. 이후 PC의 MQTT 포트 LISTEN 및 장비 → 기존 PC 앱의 ESTABLISHED 연결을 확인했습니다.
- 초기 실제 상태: collector 연결됨, online=true, ATX LED=true, 접속 수=1. 시험 도구가 WebRTC 세션이나 전원 명령을 만들지는 않았습니다. 별도 source-IP 제한 PC 호환 수신기에서 세션 메시지 3개를 확인했고, subscribe=0 / retain=0이었습니다.
- 에이전트 강제 종료 후 새 프로세스 생성·상태 초기화·장비 재접속·실제 PC 앱 중계 재연결을 확인했습니다. 이후 최종 실행 파일을 배치하고 인증 API·relay 정상 연결을 다시 확인했습니다.
- **실측 한계:** 이 장비 펌웨어는 동일한 ATX LED 상태의 반복 발행을 생략합니다. 최초 ATX 관측은 수신했으나 시간이 지나면 `stale`, 에이전트 재시작 후 새 ATX 발행이 없으면 `unknown`이 됩니다. 접속 수와 online 보고는 계속 갱신됩니다. 이 상황에서 캐시를 최신으로 바꾸거나 전원 꺼짐으로 추정하지 않습니다. 안정적인 현재 전원 상태 조회를 위해서는 펌웨어 발행 동작 보완 또는 별도 읽기 전용 갱신 경로가 후속 과제입니다.

장비 안에서 사용하는 서비스 명령:

```sh
sh /userdata/jetkvm-status-agent/service.sh status
sh /userdata/jetkvm-status-agent/service.sh stop
sh /userdata/jetkvm-status-agent/service.sh start
sh /userdata/jetkvm-status-agent/service.sh restart
```

서비스는 에이전트 프로세스 종료 시 3초 후 재실행하고, 명시적 stop에서는 다시 시작하지 않습니다. 상태 파일과 마지막 실행 로그는 `/tmp/jetkvm-status-agent/`에 두며 child 재실행 시 로그를 새로 만듭니다. 프로세스가 살아 있으나 응답하지 않는 장애를 감지하는 watchdog은 아닙니다.

MQTT 전환을 되돌릴 때는 먼저 서비스 stop 및 부팅 진입점의 `S23` 접두사 해제 후, 백업한 `kvm_config.before.json`과 현재 설정을 비교해 기존 MQTT 항목만 복원하고 펌웨어 앱에 적용하세요. 백업 전체를 무조건 덮어쓰면 설치 후 바꾼 다른 장비 설정까지 되돌아갈 수 있습니다. 이번 작업에서는 전체 제거·복원·업데이트 실패 복구를 실험하지 않았습니다.

API는 현재 **장비 loopback 전용**입니다. SSH 터널을 사용하면 로컬에서 인증 조회할 수 있습니다. Android에서 직접 사용하려면 별도로 신뢰 가능한 HTTPS 인증서·접근 경로를 구성해야 하며, 이번 작업에서는 앱의 에이전트 주소·토큰을 설정하지 않았습니다.

로컬 비공개 증거는 `.local/deploy/`에 보관하고 Git에서 제외했습니다. `scripts/pc-relay-probe.py`는 기존 수신 포트가 비어 있을 때만 사용하며, 장비 출발지 IP를 제한한 임시 PC 호환 수신기입니다. 기존 앱이 포트를 열고 있으면 실행하지 마세요.

## 다음 단계

1. ATX 변경 생략 시에도 현재 전원을 확인할 수 있는 읽기 전용 갱신 경로
2. 장시간 자원 측정, 물리 재부팅 후 자동 실행·MQTT 수집 확인
3. 기존 설정 복원·제거 검증과 PC 모니터 실제 전환 시험
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
