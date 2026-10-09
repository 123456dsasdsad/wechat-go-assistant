#!/usr/bin/env python3
"""Offline CPU transcription. No network access is needed at task execution."""
import json, os, pathlib, sys, tempfile, wave

def main():
    import pilk
    from faster_whisper import WhisperModel
    src, dst = map(pathlib.Path, sys.argv[1:3])
    model = pathlib.Path(__file__).resolve().parent / 'models/base'
    if not src.is_file() or not model.is_dir():
        raise RuntimeError('transcription_input_or_model_missing')
    with tempfile.TemporaryDirectory(prefix='wechat-voice-') as tmp:
        audio = src
        if src.suffix.lower() == '.silk':
            pcm = pathlib.Path(tmp) / 'voice.pcm'
            audio = pathlib.Path(tmp) / 'voice.wav'
            pilk.decode(str(src), str(pcm), pcm_rate=24000)
            with wave.open(str(audio), 'wb') as out:
                out.setparams((1, 2, 24000, 0, 'NONE', 'not compressed'))
                out.writeframes(pcm.read_bytes())
        engine = WhisperModel(str(model), device='cpu', compute_type='int8', cpu_threads=4, num_workers=1, local_files_only=True)
        segments, info = engine.transcribe(str(audio), beam_size=5, vad_filter=True, word_timestamps=True, condition_on_previous_text=False)
        rows = [{'start': s.start, 'end': s.end, 'text': s.text.strip()} for s in segments]
        # Silence is explicitly empty; never synthesize an instruction.
        result = {'engine': 'faster-whisper-base-cpu-int8', 'source': src.name, 'language': info.language, 'duration': info.duration, 'segments': rows}
        temp = dst.with_suffix(dst.suffix+'.partial')
        temp.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding='utf-8')
        os.replace(temp, dst)
if __name__ == '__main__':
    try: main()
    except Exception as e:
        print('transcription_failed:'+type(e).__name__, file=sys.stderr)
        sys.exit(1)
