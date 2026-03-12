import { NavLink } from "react-router-dom"


function Sidebar() {
    return (
        <div className="sidebar">
            {/*The sidebar links to each dashboard (diff style)*/}
            <NavLink className="dashboard-button" to="/" end> Dashboard </NavLink>
            {/* The sidebar links to each page*/}
            <NavLink className="sidebar-button" to="/articleView" end> Article </NavLink>
            <NavLink className="sidebar-button" to="/mediaView" end> Media </NavLink>
        </div>
    )
}

export default Sidebar