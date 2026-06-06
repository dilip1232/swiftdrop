<script lang="ts">
  let visible = $state(false);
  let message = $state("");
  let isErr = $state(false);
  let timeout: ReturnType<typeof setTimeout>;

  export function show(msg: string, err = false) {
    message = msg;
    isErr = err;
    visible = true;
    clearTimeout(timeout);
    timeout = setTimeout(() => { visible = false; }, 2600);
  }
</script>

<div class="toast" class:show={visible} class:err={isErr}>{message}</div>

<style>
  .toast {
    position: fixed; left: 50%; bottom: 16px; transform: translateX(-50%) translateY(16px);
    background: var(--panel);
    border: 1px solid var(--border); color: var(--text);
    padding: 9px 15px; border-radius: 11px; box-shadow: var(--shadow); font-size: 12.5px;
    opacity: 0; pointer-events: none; transition: .25s; max-width: 90vw;
    z-index: 1001;
  }
  .toast.show { opacity: 1; transform: translateX(-50%) translateY(0); }
  .toast.err { border-color: var(--err); }
</style>
