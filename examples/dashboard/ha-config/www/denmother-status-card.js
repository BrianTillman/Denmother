// Loaded directly by Home Assistant; no build step or external dependencies.
class DenmotherStatusCard extends HTMLElement {
  setConfig(config) {
    if (!config.entity) throw new Error("Specify an entity");
    this.config = config;
    if (!this.card) {
      this.attachShadow({ mode: "open" });
      this.card = document.createElement("ha-card");
      this.card.header = "Local Custom Card";
      this.content = document.createElement("div");
      this.content.style.padding = "16px";
      this.card.append(this.content);
      this.shadowRoot.append(this.card);
    }
  }
  set hass(hass) {
    if (!this.config) return;
    const entity = hass.states[this.config.entity];
    this.content.textContent = entity
      ? `${entity.attributes.friendly_name || this.config.entity}: ${entity.state} ${entity.attributes.unit_of_measurement || ""}`
      : "Entity not found";
  }
  getCardSize() { return 2; }
}
customElements.define("denmother-status-card", DenmotherStatusCard);
