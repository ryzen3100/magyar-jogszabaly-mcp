# Privacy & Client Confidentiality

**IMPORTANT READING FOR LEGAL PROFESSIONALS**

This document addresses privacy and confidentiality considerations when using this Tool, with particular attention to professional obligations under Hungarian bar association rules.

---

## Executive Summary

**Key Risks:**
- Queries through Claude API flow via Anthropic cloud infrastructure
- Query content may reveal client matters and privileged information
- Hungarian Bar Association rules (Magyar Ügyvédi Kamara — MÜK, muk.hu) require strict confidentiality (ügyvédi titoktartás) under the 2017. évi LXXVIII. törvény az ügyvédi tevékenységről, §9

**Safe Use Options:**
1. **General Legal Research**: Use Tool for non-client-specific queries
2. **Local Go Binary**: Install via `go install github.com/ryzen3100/magyar-jogszabaly-mcp/v2/cmd/hungarian-law-mcp@latest` — database queries stay on your machine
3. **Remote Endpoint** (1.x only — not deployed for the Go server): Vercel Streamable HTTP endpoint — queries transit Vercel infrastructure
4. **On-Premise Deployment**: Self-host with local LLM for privileged matters

---

## Data Flows and Infrastructure

### MCP (Model Context Protocol) Architecture

This Tool uses the **Model Context Protocol (MCP)** to communicate with AI clients:

```
User Query -> MCP Client (Claude Desktop/Cursor/API) -> Anthropic Cloud -> MCP Server -> Database
```

### Deployment Options

#### 1. Local Go Binary (Most Private)

```bash
go install github.com/ryzen3100/magyar-jogszabaly-mcp/v2/cmd/hungarian-law-mcp@latest
hungarian-law-mcp
```

- Database is local SQLite file on your machine
- No data transmitted to external servers (except to AI client for LLM processing)
- Full control over data at rest
- Recommended for: general research, solo practitioners, matters involving any client context

#### 2. Remote Endpoint (Vercel) — 1.x only

> The Vercel deployment (`hungarian-law-mcp.vercel.app`) is not part of the Go server; self-host instead (option 3 below).

```
Endpoint: https://hungarian-law-mcp.vercel.app/mcp
```

- Queries transit Vercel infrastructure (Vercel, Inc., USA)
- Tool responses return through the same path
- Subject to Vercel's privacy policy
- Acceptable for: fully anonymized, non-client-specific legal research only

#### 3. On-Premise Deployment (Most Secure)

```bash
docker run -p 3000:3000 ghcr.io/ryzen3100/magyar-jogszabaly-mcp
```

- Full control: no data leaves your infrastructure
- Pair with a self-hosted LLM (e.g., Ollama) to eliminate all external data flows
- Required for: classified matters, government use, matters where titoktartási kötelezettség (confidentiality obligation) mandates no external processing

### What Gets Transmitted

When you use this Tool through an AI client:

- **Query Text**: Your search queries and tool parameters
- **Tool Responses**: Statute text (jogszabályszövegek), provision content, search results
- **Metadata**: Timestamps, request identifiers

**What Does NOT Get Transmitted:**
- Files on your computer
- Your full conversation history (depends on AI client configuration)

---

## Professional Obligations (Hungary)

### Hungarian Bar Association Rules

Hungarian lawyers (ügyvédek) are bound by strict confidentiality rules under the **2017. évi LXXVIII. törvény az ügyvédi tevékenységről** (Act LXXVIII of 2017 on Legal Practice) and the **MÜK ügyvédi etikai szabályzata**, enforced by the Magyar Ügyvédi Kamara (MÜK, muk.hu). Disciplinary matters are handled by the MÜK Fegyelmi Bizottsága.

#### Titoktartási Kötelezettség (Duty of Confidentiality) — Ügyvédi Törvény §9

- All client communications are privileged under the Act on Legal Practice §9
- The duty applies without time limit and survives termination of the mandate (megbízási jogviszony)
- Client identity may be confidential in sensitive matters
- Case strategy, legal analysis, and factual instructions are protected
- Information that could identify clients or matters must be safeguarded even in anonymized queries
- Breach of confidentiality may result in disciplinary proceedings (fegyelmi eljárás) before the MÜK Fegyelmi Bizottsága and potential criminal liability under the Btk.

### Hungarian Personal Data Protection Act (Infotv.) and GDPR

Under **GDPR Article 28** and the **2011. évi CXII. törvény az információs önrendelkezési jogról és az információszabadságról (Infotv.)** — as amended to implement GDPR — when using services that process client data:

- You are the **Data Controller** (adatkezelő) under GDPR Article 4(7) and Infotv.
- AI service providers (Anthropic, Vercel) may be **Data Processors** (adatfeldolgozó) under GDPR Article 4(8) and Infotv.
- A **Data Processing Agreement** (adatfeldolgozási megállapodás) under GDPR Article 28 and Infotv. may be required before transmitting any personal data
- Ensure adequate technical and organizational measures (technikai és szervezési intézkedések) are in place
- The **Nemzeti Adatvédelmi és Információszabadság Hatóság (NAIH, naih.hu)** is the supervisory authority for Hungarian GDPR and Infotv. compliance; NAIH handles complaints, investigations, and enforcement including fines

### Infotv. — Specific Hungarian Provisions

The 2011. évi CXII. törvény (Infotv.) predates GDPR and was significantly amended to accommodate it. Key Hungarian-specific provisions include:

- Rules on information self-determination rights (információs önrendelkezési jog) applicable alongside GDPR rights
- Freedom of information (közérdekű adatok nyilvánossága) provisions for public sector data
- Rules on natural person identification numbers (természetes személyazonosító adatok), which have heightened protection
- Specific obligations for processing by public authorities and courts

Ügyvédek processing client personal data must comply with both GDPR and Infotv. When in doubt, consult NAIH guidance at naih.hu, including published ajánlások (recommendations) and határozatok (decisions).

---

## Risk Assessment by Use Case

### LOW RISK: General Legal Research

**Safe to use through any deployment:**

```
Example: "What does § 6:519 of the Ptk. (2013. évi V. törvény) say about general tort liability?"
```

- No client identity involved
- No case-specific facts
- Publicly available legal information

### MEDIUM RISK: Anonymized Queries

**Use with caution:**

```
Example: "What are the penalties for fraud (csalás) under the Hungarian Btk. (2012. évi C. törvény)?"
```

- Query pattern may reveal you are working on a fraud matter
- Anthropic/Vercel logs may link queries to your API key
- Consider using the local Go binary even for anonymized queries involving sensitive practice areas

### HIGH RISK: Client-Specific Queries

**DO NOT USE through cloud AI services:**

- Remove ALL identifying details before using any cloud deployment
- Use the local Go binary with a self-hosted LLM
- Or use commercial legal databases (Complex, Jogtár) with proper adatfeldolgozási megállapodások
- Queries containing client names, személyi azonosítószámok (personal identification numbers), cégjegyzékszámok (company registration numbers), or case references are HIGH RISK even if you consider them anonymized

---

## Data Collection by This Tool

### What This Tool Collects

**Nothing.** This Tool:

- Does NOT log queries
- Does NOT store user data
- Does NOT track usage
- Does NOT use analytics
- Does NOT set cookies

The database is read-only. No user data is written to disk.

### What Third Parties May Collect

- **Anthropic** (if using Claude): Subject to [Anthropic Privacy Policy](https://www.anthropic.com/legal/privacy)
- **Vercel** (if using remote endpoint): Subject to [Vercel Privacy Policy](https://vercel.com/legal/privacy-policy)

---

## Recommendations

### For Solo Practitioners / Small Firms (Egyéni ügyvédek / Kisebb irodák)

1. Use the local Go binary for maximum privacy
2. General research: Cloud AI is acceptable for fully non-client-specific queries
3. Client matters: Use commercial legal databases (Complex, Jogtár) with proper adatfeldolgozási megállapodások under GDPR Article 28 and Infotv.
4. Review MÜK ethics guidance on AI tool use before adopting any cloud-based legal AI tool

### For Large Firms / Corporate Legal (Nagy irodák / Vállalati jogi osztályok)

1. Negotiate Data Processing Agreements (adatfeldolgozási megállapodások) with AI service providers before use
2. Consider on-premise deployment with self-hosted LLM for client-facing work
3. Train staff on safe vs. unsafe query patterns — include in annual GDPR and Infotv. compliance training
4. Designate a Data Protection Officer (adatvédelmi tisztviselő) if required under GDPR Article 37 and Infotv.

### For Government / Public Sector (Állami szervek / Közszféra)

1. Use self-hosted deployment, no external APIs
2. Follow Hungarian government IT security requirements under the **2013. évi L. törvény az állami és önkormányzati szervek elektronikus információbiztonságáról (Ibtv.)** and SZTFH (Szabályozott Tevékenységek Felügyeleti Hatósága) requirements
3. Air-gapped option available for matters classified under the **2009. évi CLV. törvény a minősített adat védelméről** (Act on the Protection of Classified Information)

---

## Questions and Support

- **Privacy Questions**: Open issue on [GitHub](https://github.com/ryzen3100/magyar-jogszabaly-mcp/issues)
- **Anthropic Privacy**: Contact privacy@anthropic.com
- **MÜK Guidance**: Consult the Magyar Ügyvédi Kamara (muk.hu) for ethics guidance on AI tool use by ügyvédek
- **NAIH**: For GDPR and Infotv. compliance queries, see naih.hu

---

**Last Updated**: 2026-08-27
**Tool Version**: 2.0.0
