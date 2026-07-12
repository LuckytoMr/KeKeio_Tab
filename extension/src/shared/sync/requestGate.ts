export type ScopedRequestToken = {
  scope: string;
  generation: number;
};

export class ScopedRequestGate {
  private generation = 0;
  private scope = "";

  begin(scope: string): ScopedRequestToken {
    this.scope = scope;
    this.generation += 1;
    return { scope, generation: this.generation };
  }

  isCurrent(token: ScopedRequestToken) {
    return token.scope === this.scope && token.generation === this.generation;
  }

  invalidate() {
    this.scope = "";
    this.generation += 1;
  }
}
