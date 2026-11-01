// 近接音声通話の WebRTC ラッパー（P2P メッシュ）。Go（WASM）から syscall/js で呼ぶ。設計: docs/design.md §11
//
// 約束:
//  - Go 側の呼び出しの途中でコールバックを同期的に返さない（onsignal / onpeerclosed は必ず非同期に呼ぶ）
//  - 相手は pid（ルーム内の番号）で指す。offer を作る側は Go が決める（pid の小さい側）
//  - シグナリングの中身は JSON 文字列。サーバは中継するだけ
(() => {
  const ICE_SERVERS = [{ urls: "stun:stun.l.google.com:19302" }]; // TURN は後回し
  const CONNECT_TIMEOUT_MS = 15000;
  const MAX_SIGNAL_BYTES = 12000; // サーバの上限（maxSignalPayload）に合わせる

  let ctx = null;
  let local = null; // マイクの MediaStream
  let muted = false;
  let camTrack = null; // カメラの映像トラック（オフなら null）
  let selfTile = null;
  const peers = new Map(); // pid -> {pc, gain, audio, pending, remoteSet, timer}

  const later = (f) => setTimeout(f, 0);
  const voice = (window.gopherVoice = {
    onsignal: null, // (pid, json) => void
    onpeerclosed: null, // (pid) => void

    async start() {
      if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
        throw new Error("このブラウザはマイクを使えません（HTTPS が必要です）");
      }
      local = await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
      });
      ctx = new (window.AudioContext || window.webkitAudioContext)();
      await ctx.resume(); // 利用者の操作（キー入力）の直後に呼ばれる前提
      voice.setMuted(muted);
    },

    stop() {
      voice.setCamera(false);
      for (const pid of [...peers.keys()]) voice.disconnect(pid);
      if (local) local.getTracks().forEach((t) => t.stop());
      local = null;
      if (ctx) ctx.close();
      ctx = null;
    },

    setMuted(m) {
      muted = m;
      if (local) local.getAudioTracks().forEach((t) => (t.enabled = !m));
    },

    // デバッグ用：相手との接続状態（"connected" など）。接続が無ければ "none"
    state(pid) {
      const p = peers.get(pid);
      return p ? p.pc.connectionState : "none";
    },

    setGain(pid, g) {
      const p = peers.get(pid);
      if (!p || !ctx) return;
      p.gain.gain.setTargetAtTime(g, ctx.currentTime, 0.1);
      p.level = g;
      showTile(p);
    },

    // 映像の枠に出す名前
    setLabel(pid, name) {
      const p = peers.get(pid);
      if (p) p.tile.label.textContent = name;
    },

    // カメラを入れる／切る。入れるときは Promise（許可を待つ）。接続の張り直しはしない（replaceTrack）
    async setCamera(on) {
      if (!on) {
        if (camTrack) camTrack.stop();
        camTrack = null;
        for (const [pid, p] of peers) {
          p.vsender.replaceTrack(null).catch(() => {});
          emit(pid, { camera: false });
        }
        if (selfTile) selfTile.root.remove();
        selfTile = null;
        return;
      }
      if (!local || camTrack) return;
      const s = await navigator.mediaDevices.getUserMedia({ video: { width: 320, height: 240, frameRate: 15 } });
      camTrack = s.getVideoTracks()[0];
      for (const [pid, p] of peers) {
        p.vsender.replaceTrack(camTrack).catch(() => {});
        emit(pid, { camera: true });
      }
      selfTile = makeTile("You");
      selfTile.video.srcObject = new MediaStream([camTrack]);
      selfTile.video.style.transform = "scaleX(-1)"; // 自分の映像は鏡像
      selfTile.root.style.display = "block";
    },

    connect(pid, initiator) {
      if (!ctx || !local || peers.has(pid)) return;
      const p = newPeer(pid);
      if (initiator) later(() => offer(pid, p));
    },

    disconnect(pid) {
      const p = peers.get(pid);
      if (!p) return;
      peers.delete(pid); // 先に消す：意図した切断では onpeerclosed を呼ばない
      clearTimeout(p.timer);
      p.pc.close();
      p.gain.disconnect();
      p.audio.srcObject = null;
      p.tile.root.remove();
    },

    // 相手から届いたシグナリング（JSON 文字列）。接続がまだ無ければ answer する側として作る
    async signal(pid, json) {
      if (!ctx || !local) return;
      let msg;
      try {
        msg = JSON.parse(json);
      } catch {
        return;
      }
      let p = peers.get(pid);
      if (!p) p = newPeer(pid);
      if (typeof msg.camera === "boolean") {
        p.hasVideo = msg.camera;
        showTile(p);
        return;
      }
      try {
        if (msg.description) {
          await p.pc.setRemoteDescription(msg.description);
          p.remoteSet = true;
          for (const c of p.pending.splice(0)) await p.pc.addIceCandidate(c);
          if (msg.description.type === "offer") {
            await p.pc.setLocalDescription(await p.pc.createAnswer());
            emit(pid, { description: p.pc.localDescription });
          }
        } else if (msg.candidate) {
          if (p.remoteSet) await p.pc.addIceCandidate(msg.candidate);
          else p.pending.push(msg.candidate);
        }
      } catch (e) {
        console.warn("voice: signal failed", pid, e);
        closed(pid, p);
      }
    },
  });

  function newPeer(pid) {
    const pc = new RTCPeerConnection({ iceServers: ICE_SERVERS });
    const gain = ctx.createGain();
    gain.gain.value = 0; // 距離が決まるまで無音
    gain.connect(ctx.destination);
    const audio = new Audio(); // Chrome は、これに繋がないと WebAudio に音が流れない
    audio.muted = true;
    // 映像の送受信口は最初から作っておく（後から足すと再ネゴシエーションが要る）。カメラが入ったら replaceTrack で載せる
    local.getTracks().forEach((t) => pc.addTrack(t, local));
    const vt = pc.addTransceiver("video", { direction: "sendrecv" });
    preferVP8(vt); // 全コーデックを並べると SDP が 6KB を超える。VP8 だけにして小さくする
    const vsender = vt.sender;
    if (camTrack) vsender.replaceTrack(camTrack).catch(() => {});
    const p = { pc, gain, audio, vsender, tile: makeTile(""), level: 0, hasVideo: false, pending: [], remoteSet: false, timer: 0 };
    peers.set(pid, p);

    pc.ontrack = (ev) => {
      if (ev.track.kind === "video") {
        // 相手がカメラを入れると unmute、切ると mute になる
        p.tile.video.srcObject = new MediaStream([ev.track]);
        return; // 枠を出すかどうかは、相手からの {camera: bool} の通知で決める（replaceTrack(null) では mute にならない）
      }
      const stream = new MediaStream([ev.track]);
      audio.srcObject = stream;
      ctx.createMediaStreamSource(stream).connect(gain);
    };
    pc.onicecandidate = (ev) => {
      if (ev.candidate) emit(pid, { candidate: ev.candidate });
    };
    pc.onconnectionstatechange = () => {
      if (pc.connectionState === "connected") {
        clearTimeout(p.timer);
        if (camTrack) emit(pid, { camera: true }); // つながった時点でカメラが入っていれば知らせる
      }
      if (pc.connectionState === "failed" || pc.connectionState === "closed") closed(pid, p);
    };
    p.timer = setTimeout(() => {
      if (pc.connectionState !== "connected") closed(pid, p);
    }, CONNECT_TIMEOUT_MS);
    return p;
  }

  function preferVP8(t) {
    try {
      const codecs = RTCRtpReceiver.getCapabilities("video").codecs;
      const vp8 = codecs.filter((c) => /vp8|rtx|red|ulpfec/i.test(c.mimeType));
      if (vp8.some((c) => /vp8/i.test(c.mimeType))) t.setCodecPreferences(vp8);
    } catch (e) {
      console.warn("voice: codec preference failed", e);
    }
  }

  // --- 映像の枠（DOM オーバーレイ。canvas の上、右上に縦に並べる） ---
  function overlay() {
    let el = document.getElementById("voice-tiles");
    if (!el) {
      el = document.createElement("div");
      el.id = "voice-tiles";
      el.style.cssText =
        "position:fixed;top:8px;right:8px;display:flex;flex-direction:column;gap:6px;z-index:10;pointer-events:none";
      document.body.appendChild(el);
    }
    return el;
  }

  function makeTile(name) {
    const root = document.createElement("div");
    root.style.cssText = "display:none;position:relative;width:160px;height:120px;border-radius:8px;overflow:hidden;background:#000;box-shadow:0 2px 8px #0008";
    const video = document.createElement("video");
    video.autoplay = true;
    video.muted = true; // 音は WebAudio 側で鳴らす（距離で音量を変えるため）
    video.playsInline = true;
    video.style.cssText = "width:100%;height:100%;object-fit:cover";
    const label = document.createElement("div");
    label.textContent = name;
    label.style.cssText = "position:absolute;left:0;right:0;bottom:0;padding:2px 6px;font:12px sans-serif;color:#fff;background:#0008;white-space:nowrap;overflow:hidden;text-overflow:ellipsis";
    root.append(video, label);
    overlay().appendChild(root);
    return { root, video, label };
  }

  // 相手が映像を出していて、声の届く範囲にいるときだけ見せる。遠いほど薄くする
  function showTile(p) {
    const show = p.hasVideo && p.level > 0.01;
    p.tile.root.style.display = show ? "block" : "none";
    p.tile.root.style.opacity = String(Math.max(0.4, p.level));
  }

  async function offer(pid, p) {
    try {
      await p.pc.setLocalDescription(await p.pc.createOffer());
      emit(pid, { description: p.pc.localDescription });
    } catch (e) {
      console.warn("voice: offer failed", pid, e);
      closed(pid, p);
    }
  }

  function emit(pid, msg) {
    const json = JSON.stringify(msg);
    if (json.length > MAX_SIGNAL_BYTES) return console.warn("voice: signal too large", json.length);
    if (voice.onsignal) later(() => voice.onsignal(pid, json));
  }

  // 失敗・タイムアウト。まだ自分が持っている接続のときだけ片付けて、Go に知らせる
  function closed(pid, p) {
    if (peers.get(pid) !== p) return;
    voice.disconnect(pid);
    if (voice.onpeerclosed) later(() => voice.onpeerclosed(pid));
  }
})();
