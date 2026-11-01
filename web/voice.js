// 近接音声通話の WebRTC ラッパー（P2P メッシュ）。Go（WASM）から syscall/js で呼ぶ。設計: docs/design.md §11
//
// 約束:
//  - Go 側の呼び出しの途中でコールバックを同期的に返さない（onsignal / onpeerclosed は必ず非同期に呼ぶ）
//  - 相手は pid（ルーム内の番号）で指す。offer を作る側は Go が決める（pid の小さい側）
//  - シグナリングの中身は JSON 文字列。サーバは中継するだけ
(() => {
  const ICE_SERVERS = [{ urls: "stun:stun.l.google.com:19302" }]; // TURN は後回し
  const CONNECT_TIMEOUT_MS = 15000;
  const MAX_SIGNAL_BYTES = 6000; // サーバの上限（maxSignalPayload）に合わせる

  let ctx = null;
  let local = null; // マイクの MediaStream
  let muted = false;
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
      if (p && ctx) p.gain.gain.setTargetAtTime(g, ctx.currentTime, 0.1);
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
    const p = { pc, gain, audio, pending: [], remoteSet: false, timer: 0 };
    peers.set(pid, p);

    local.getTracks().forEach((t) => pc.addTrack(t, local));
    pc.ontrack = (ev) => {
      const stream = ev.streams[0] || new MediaStream([ev.track]);
      audio.srcObject = stream;
      ctx.createMediaStreamSource(stream).connect(gain);
    };
    pc.onicecandidate = (ev) => {
      if (ev.candidate) emit(pid, { candidate: ev.candidate });
    };
    pc.onconnectionstatechange = () => {
      if (pc.connectionState === "connected") clearTimeout(p.timer);
      if (pc.connectionState === "failed" || pc.connectionState === "closed") closed(pid, p);
    };
    p.timer = setTimeout(() => {
      if (pc.connectionState !== "connected") closed(pid, p);
    }, CONNECT_TIMEOUT_MS);
    return p;
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
