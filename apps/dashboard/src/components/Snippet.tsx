import { useEffect, useRef, useState } from 'react';

type CopyState = 'idle' | 'copied' | 'selected';

/**
 * A copyable code sample.
 *
 * Documentation people copy from is documentation that gets used, and a
 * developer retyping a curl command will introduce a typo the first time.
 *
 * The clipboard is not assumed to exist. PayMux is self-hosted, and an
 * instance served over plain HTTP on a LAN address is not a secure context, so
 * navigator.clipboard is simply absent there, as it is when a permissions
 * policy blocks it. Rather than a button that silently does nothing, the
 * fallback selects the text and says so, which leaves the reader one keystroke
 * from the same result.
 */
export function Snippet({ label, code }: { label?: string; code: string }) {
  const [state, setState] = useState<CopyState>('idle');
  const codeRef = useRef<HTMLPreElement>(null);

  useEffect(() => {
    if (state === 'idle') return;
    const timer = setTimeout(() => setState('idle'), 2200);
    return () => clearTimeout(timer);
  }, [state]);

  const selectInstead = () => {
    const node = codeRef.current;
    if (!node) return;
    const range = document.createRange();
    range.selectNodeContents(node);
    const selection = window.getSelection();
    selection?.removeAllRanges();
    selection?.addRange(range);
    setState('selected');
  };

  const copy = async () => {
    if (!navigator.clipboard?.writeText) {
      selectInstead();
      return;
    }
    try {
      await navigator.clipboard.writeText(code);
      setState('copied');
    } catch {
      selectInstead();
    }
  };

  return (
    <div className="snippet">
      <div className="snippet__bar">
        <span className="snippet__label">{label ?? 'shell'}</span>
        <button type="button" className="button button--small" onClick={copy}>
          {state === 'copied' ? 'Copied' : state === 'selected' ? 'Press Ctrl+C' : 'Copy'}
        </button>
      </div>
      <pre className="snippet__code" ref={codeRef}>
        {code}
      </pre>
    </div>
  );
}
