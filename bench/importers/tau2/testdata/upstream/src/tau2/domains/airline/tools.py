# Hand-written fixture in the shape of an upstream domain tools file.
class FixtureAirlineTools:
    @is_tool(ToolType.READ)
    def get_booking(self, booking_id: str):
        return {}

    @is_tool(ToolType.WRITE)
    def cancel_booking(self, booking_id: str):
        return {}

    @is_tool(ToolType.WRITE, mutates_state=False)
    def transfer_to_human_agents(self, summary: str):
        return "Transfer successful"

    @is_tool(ToolType.GENERIC, mutates_state=True)
    def add_note(self, booking_id: str, note: str):
        return {}
