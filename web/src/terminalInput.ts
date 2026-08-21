export class TerminalInputGuard {
  private activeTurn = 0;
  private consumedTurn = 0;
  private lastKey = { signature: "", timestamp: -1 };
  private nextTurn = 1;

  startKey(signature: string, timestamp: number): number {
    if (
      signature === this.lastKey.signature &&
      timestamp === this.lastKey.timestamp
    ) {
      return 0;
    }
    this.lastKey = { signature, timestamp };
    this.activeTurn = this.nextTurn++;
    return this.activeTurn;
  }

  endKey(turn: number): void {
    if (this.activeTurn === turn) this.activeTurn = 0;
  }

  acceptData(): boolean {
    if (this.activeTurn === 0) return true;
    if (this.consumedTurn === this.activeTurn) return false;
    this.consumedTurn = this.activeTurn;
    return true;
  }
}
